package h264

import "fmt"

// Decoder owns the parameter sets and MP4 NAL framing configuration needed
// to decode successive AVC samples.
type Decoder struct {
	nalLengthSize int
	store         *ParameterSetStore
	dpb           []referencePicture
}

// NewDecoder creates a decoder from an AVCDecoderConfigurationRecord.
func NewDecoder(config AVCConfig) (*Decoder, error) {
	store, err := StoreFromConfig(config)
	if err != nil {
		return nil, err
	}
	return &Decoder{nalLengthSize: config.NALLengthSize, store: store}, nil
}

// DecodeSample parses one MP4 access unit, applies in-band parameter-set
// updates (avc3), and decodes its picture.
func (d *Decoder) DecodeSample(sample []byte) (*Frame420, error) {
	if d == nil || d.store == nil {
		return nil, fmt.Errorf("nil H.264 decoder")
	}
	units, err := ParseSample(sample, d.nalLengthSize)
	if err != nil {
		return nil, err
	}
	for _, unit := range units {
		switch unit.Type {
		case NALSPS:
			if _, err = d.store.AddSPS(unit); err != nil {
				return nil, err
			}
		case NALPPS:
			if _, err = d.store.AddPPS(unit); err != nil {
				return nil, err
			}
		}
	}
	var firstSlice *NALUnit
	for i := range units {
		if units[i].Type == NALSliceIDR || units[i].Type == NALSliceNonIDR {
			firstSlice = &units[i]
			break
		}
	}
	if firstSlice == nil {
		return nil, fmt.Errorf("sample has no coded slice NAL units")
	}
	header, err := ParseSliceHeader(*firstSlice, d.store)
	if err != nil {
		return nil, err
	}
	var frame *Frame420
	switch header.Type {
	case SliceI:
		if header.PPS.EntropyCodingCABAC {
			frame, err = DecodeCABACIFrame(units, d.store)
		} else {
			frame, err = DecodeIDRFrame(units, d.store)
		}
	case SliceP:
		var references []*Frame420
		references, err = d.buildPReferenceList(header)
		if err == nil {
			if header.PPS.EntropyCodingCABAC {
				frame, err = DecodeCABACPSkipFrame(units, d.store, references)
			} else {
				frame, err = DecodePFrame(units, d.store, references)
			}
		}
	case SliceB:
		var references [2][]*Frame420
		references, err = d.buildBReferenceLists(header)
		if err == nil {
			if header.PPS.EntropyCodingCABAC {
				frame, err = DecodeCABACBSkipFrame(units, d.store, references)
			} else {
				frame, err = DecodeBFrame(units, d.store, references)
			}
		}
	default:
		err = fmt.Errorf("unsupported %s-slice picture", header.Type)
	}
	if err != nil {
		return nil, err
	}
	frame.poc = int64(header.PictureOrderCountLSB)
	if firstSlice.RefIDC != 0 {
		if err = d.markReference(frame, header, firstSlice.Type == NALSliceIDR); err != nil {
			return nil, err
		}
	}
	return frame, nil
}

// DecodeBFrame decodes supported CAVLC B macroblocks, including split and
// direct prediction modes.
func DecodeBFrame(units []NALUnit, store *ParameterSetStore, references [2][]*Frame420) (*Frame420, error) {
	if len(references[0]) == 0 || len(references[1]) == 0 || references[0][0] == nil || references[1][0] == nil {
		return nil, fmt.Errorf("B picture has incomplete reference lists")
	}
	frame, err := NewFrame420(references[0][0].Width, references[0][0].Height)
	if err != nil {
		return nil, err
	}
	frame.displayX, frame.displayY = references[0][0].displayX, references[0][0].displayY
	frame.displayWidth, frame.displayHeight = references[0][0].displayWidth, references[0][0].displayHeight
	mbCount, mbWidth := frame.Width/16*(frame.Height/16), frame.Width/16
	decoded := make(map[int]bool)
	context, _ := NewCAVLCBlockContext(mbWidth)
	motion := [2]map[[2]int]motionInfo{make(map[[2]int]motionInfo), make(map[[2]int]motionInfo)}
	for _, unit := range units {
		if unit.Type != NALSliceNonIDR {
			continue
		}
		header, parseErr := ParseSliceHeader(unit, store)
		if parseErr != nil {
			return nil, parseErr
		}
		if header.Type != SliceB || header.PPS.EntropyCodingCABAC || !header.SPS.FrameMbsOnly || header.SPS.ChromaFormat != 1 {
			return nil, fmt.Errorf("unsupported B picture profile")
		}
		r, readErr := SliceDataReader(unit, header)
		if readErr != nil {
			return nil, readErr
		}
		address, qp := int(header.FirstMacroblock), int(header.SliceQP)
		for r.MoreRBSPData() {
			run, runErr := r.ReadUE()
			if runErr != nil {
				return nil, malformed("B mb_skip_run is truncated")
			}
			if run > uint64(mbCount-address) {
				return nil, malformed("B mb_skip_run exceeds picture")
			}
			for range int(run) {
				if address < 0 || address >= mbCount || decoded[address] {
					return nil, malformed("B_Skip macroblock address is invalid or duplicated")
				}
				x, y := address%mbWidth, address/mbWidth
				partitions, directErr := deriveDirectPartitions(header, references, motion, x, y, InterPartition{Width: 16, Height: 16, Direct: true})
				if directErr != nil {
					return nil, fmt.Errorf("B_Skip macroblock %d: %w", address, directErr)
				}
				if err = reconstructBMacroblock(frame, references, context, x, y, qp, header, MacroblockHeader{Kind: MacroblockInter}, partitions, r, false); err != nil {
					return nil, fmt.Errorf("B_Skip macroblock %d: %w", address, err)
				}
				recordBMotion(motion, x, y, partitions, references)
				decoded[address] = true
				address++
			}
			if !r.MoreRBSPData() {
				continue
			}
			if address < 0 || address >= mbCount || decoded[address] {
				return nil, malformed("B macroblock address is invalid or duplicated")
			}
			h, headerErr := ParseMacroblockHeader(r, header)
			if headerErr != nil {
				return nil, fmt.Errorf("B macroblock %d: %w", address, headerErr)
			}
			if h.Kind != MacroblockInter || len(h.InterPartitions) == 0 {
				return nil, fmt.Errorf("B macroblock %d uses unsupported prediction", address)
			}
			qp = (qp + int(h.QPDelta) + 52) % 52
			x, y := address%mbWidth, address/mbWidth
			resolved := make([]resolvedBPartition, 0, len(h.InterPartitions))
			for index, partition := range h.InterPartitions {
				if partition.Direct {
					direct, directErr := deriveDirectPartitions(header, references, motion, x, y, partition)
					if directErr != nil {
						return nil, fmt.Errorf("B Direct macroblock %d: %w", address, directErr)
					}
					resolved = append(resolved, direct...)
					continue
				}
				resolved = append(resolved, resolvedBPartition{InterPartition: partition})
				resolvedIndex := len(resolved) - 1
				predictionShape := uint64(0)
				if partition.Height == 8 {
					predictionShape = 1
				} else if partition.Width == 8 {
					predictionShape = 2
				}
				if partition.UseList0 {
					if partition.ReferenceIndex >= uint64(len(references[0])) {
						return nil, fmt.Errorf("B list-0 reference index is unavailable")
					}
					predictor := predictPartitionMotion(motion[0], x, y, predictionShape, index, partition)
					resolved[resolvedIndex].MotionL0 = [2]int{predictor[0] + int(partition.MotionDifference[0]), predictor[1] + int(partition.MotionDifference[1])}
					recordMotionPartitions(motion[0], x, y, []resolvedInterPartition{{InterPartition: partition, Motion: resolved[resolvedIndex].MotionL0, Reference: references[0][partition.ReferenceIndex]}})
				}
				if partition.UseList1 {
					if partition.ReferenceIndexL1 >= uint64(len(references[1])) {
						return nil, fmt.Errorf("B list-1 reference index is unavailable")
					}
					p1 := partition
					p1.ReferenceIndex = partition.ReferenceIndexL1
					predictor := predictPartitionMotion(motion[1], x, y, predictionShape, index, p1)
					resolved[resolvedIndex].MotionL1 = [2]int{predictor[0] + int(partition.MotionDifferenceL1[0]), predictor[1] + int(partition.MotionDifferenceL1[1])}
					recordMotionPartitions(motion[1], x, y, []resolvedInterPartition{{InterPartition: p1, Motion: resolved[resolvedIndex].MotionL1, Reference: references[1][partition.ReferenceIndexL1]}})
				}
			}
			if err = reconstructBMacroblock(frame, references, context, x, y, qp, header, h, resolved, r, true); err != nil {
				return nil, fmt.Errorf("B macroblock %d: %w", address, err)
			}
			recordBMotion(motion, x, y, resolved, references)
			decoded[address] = true
			address++
		}
	}
	if len(decoded) != mbCount {
		return nil, malformed(fmt.Sprintf("B picture contains %d of %d macroblocks", len(decoded), mbCount))
	}
	frame.motion = motion
	return frame, nil
}

type resolvedBPartition struct {
	InterPartition
	MotionL0, MotionL1 [2]int
}

func deriveDirectPartitions(header SliceHeader, references [2][]*Frame420, motion [2]map[[2]int]motionInfo, mbX, mbY int, region InterPartition) ([]resolvedBPartition, error) {
	unit := 4
	if header.SPS.Direct8x8Inference && region.Width >= 8 && region.Height >= 8 {
		unit = 8
	}
	result := make([]resolvedBPartition, 0, region.Width/unit*region.Height/unit)
	for y := region.Y; y < region.Y+region.Height; y += unit {
		for x := region.X; x < region.X+region.Width; x += unit {
			partition := resolvedBPartition{InterPartition: InterPartition{X: x, Y: y, Width: unit, Height: unit, UseList0: true, UseList1: true, Direct: true}}
			blockX, blockY := mbX*4+x/4, mbY*4+y/4
			var err error
			if header.DirectSpatialPrediction {
				partition = spatialDirectPartition(partition, references, motion, blockX, blockY)
			} else {
				partition, err = temporalDirectPartition(partition, header, references, blockX, blockY)
			}
			if err != nil {
				return nil, err
			}
			result = append(result, partition)
		}
	}
	return result, nil
}

func temporalDirectPartition(partition resolvedBPartition, header SliceHeader, references [2][]*Frame420, blockX, blockY int) (resolvedBPartition, error) {
	colocatedFrame := references[1][0]
	colocated, ok := colocatedFrame.motion[0][[2]int{blockX, blockY}]
	if !ok || colocated.intra {
		colocated, ok = colocatedFrame.motion[1][[2]int{blockX, blockY}]
	}
	if !ok || colocated.intra || colocated.picture == nil {
		partition.ReferenceIndex, partition.ReferenceIndexL1 = 0, 0
		return partition, nil
	}
	list0Index := -1
	for index, picture := range references[0] {
		if picture == colocated.picture {
			list0Index = index
			break
		}
	}
	if list0Index < 0 {
		return partition, fmt.Errorf("temporal Direct cannot map colocated reference into list 0")
	}
	partition.ReferenceIndex, partition.ReferenceIndexL1 = uint64(list0Index), 0
	if colocated.picture.longTerm {
		partition.MotionL0 = colocated.vector
		return partition, nil
	}
	td := clipInt(int(references[1][0].poc-colocated.picture.poc), -128, 127)
	tb := clipInt(int(int64(header.PictureOrderCountLSB)-colocated.picture.poc), -128, 127)
	if td == 0 {
		partition.MotionL0 = colocated.vector
		return partition, nil
	}
	tx := (16384 + absInt(td/2)) / td
	distanceScale := clipInt((tb*tx+32)>>6, -1024, 1023)
	for component := range 2 {
		partition.MotionL0[component] = (distanceScale*colocated.vector[component] + 128) >> 8
		partition.MotionL1[component] = partition.MotionL0[component] - colocated.vector[component]
	}
	return partition, nil
}

func spatialDirectPartition(partition resolvedBPartition, references [2][]*Frame420, motion [2]map[[2]int]motionInfo, blockX, blockY int) resolvedBPartition {
	colocatedFrame := references[1][0]
	colocated, hasColocated := colocatedFrame.motion[0][[2]int{blockX, blockY}]
	if !hasColocated || colocated.intra {
		colocated, hasColocated = colocatedFrame.motion[1][[2]int{blockX, blockY}]
	}
	for list := range 2 {
		reference := minimumNeighbourReference(motion[list], blockX, blockY)
		if reference == ^uint64(0) || int(reference) >= len(references[list]) {
			reference = 0
		}
		vector := predictMotionVector(motion[list], blockX, blockY, partition.Width/4, reference)
		directZero := reference == 0 && (!hasColocated || colocated.intra ||
			colocated.reference == 0 && absInt(colocated.vector[0]) <= 1 && absInt(colocated.vector[1]) <= 1)
		if directZero {
			vector = [2]int{}
		}
		if list == 0 {
			partition.ReferenceIndex, partition.MotionL0 = reference, vector
		} else {
			partition.ReferenceIndexL1, partition.MotionL1 = reference, vector
		}
	}
	return partition
}

func minimumNeighbourReference(field map[[2]int]motionInfo, x, y int) uint64 {
	minimum := ^uint64(0)
	for _, coordinate := range [][2]int{{x - 1, y}, {x, y - 1}, {x + 1, y - 1}} {
		value, ok := field[coordinate]
		if ok && !value.intra && value.reference < minimum {
			minimum = value.reference
		}
	}
	return minimum
}

func recordBMotion(fields [2]map[[2]int]motionInfo, mbX, mbY int, partitions []resolvedBPartition, references [2][]*Frame420) {
	for _, partition := range partitions {
		if partition.UseList0 {
			recordMotionPartitions(fields[0], mbX, mbY, []resolvedInterPartition{{InterPartition: partition.InterPartition, Motion: partition.MotionL0, Reference: references[0][partition.ReferenceIndex]}})
		}
		if partition.UseList1 {
			p1 := partition.InterPartition
			p1.ReferenceIndex = partition.ReferenceIndexL1
			recordMotionPartitions(fields[1], mbX, mbY, []resolvedInterPartition{{InterPartition: p1, Motion: partition.MotionL1, Reference: references[1][partition.ReferenceIndexL1]}})
		}
	}
}

func reconstructBMacroblock(frame *Frame420, references [2][]*Frame420, context *CAVLCBlockContext, mbX, mbY, qp int, slice SliceHeader, header MacroblockHeader, partitions []resolvedBPartition, r *BitReader, coded bool) error {
	var residual Intra4x4LumaResidual
	var err error
	if coded {
		residual, err = DecodeIntra4x4LumaResidual(r, header, context, mbX, mbY)
	} else {
		for block := range 16 {
			err = context.setTotalCoeff(mbX, mbY, block, 0)
		}
	}
	if err != nil {
		return err
	}
	chromaResidual, err := DecodeChromaResidual420(r, header, context, mbX, mbY)
	if err != nil {
		return err
	}
	return reconstructBMacroblockResidual(frame, references, mbX, mbY, qp, slice, partitions, residual, chromaResidual)
}

func reconstructBMacroblockResidual(frame *Frame420, references [2][]*Frame420, mbX, mbY, qp int, slice SliceHeader,
	partitions []resolvedBPartition, residual Intra4x4LumaResidual, chromaResidual ChromaResidual420,
) error {
	var luma [256]uint8
	var chroma [2][64]uint8
	for _, partition := range partitions {
		var luma0, luma1 [256]uint8
		var chroma0, chroma1 [2][64]uint8
		if partition.UseList0 {
			luma0 = predictInterLuma(references[0][partition.ReferenceIndex], mbX, mbY, partition.MotionL0)
			chroma0 = predictInterChroma(references[0][partition.ReferenceIndex], mbX, mbY, partition.MotionL0)
		}
		if partition.UseList1 {
			luma1 = predictInterLuma(references[1][partition.ReferenceIndexL1], mbX, mbY, partition.MotionL1)
			chroma1 = predictInterChroma(references[1][partition.ReferenceIndexL1], mbX, mbY, partition.MotionL1)
		}
		for y := partition.Y; y < partition.Y+partition.Height; y++ {
			for x := partition.X; x < partition.X+partition.Width; x++ {
				i := y*16 + x
				switch {
				case slice.PPS.WeightedBipredIDC == 2 && partition.UseList0 && partition.UseList1:
					luma[i] = implicitBPrediction(luma0[i], luma1[i], slice, references[0][partition.ReferenceIndex], references[1][partition.ReferenceIndexL1])
				case slice.PPS.WeightedBipredIDC == 1:
					luma[i] = weightedBPrediction(luma0[i], luma1[i], partition.UseList0, partition.UseList1,
						bWeight(slice, 0, partition.ReferenceIndex, -1), bWeight(slice, 1, partition.ReferenceIndexL1, -1), slice.LumaLog2WeightDenom)
				case partition.UseList0 && partition.UseList1:
					luma[i] = uint8((int(luma0[i]) + int(luma1[i]) + 1) >> 1)
				case partition.UseList0:
					luma[i] = luma0[i]
				default:
					luma[i] = luma1[i]
				}
			}
		}
		x0, y0, width, height := partition.X/2, partition.Y/2, partition.Width/2, partition.Height/2
		for component := range 2 {
			for y := y0; y < y0+height; y++ {
				for x := x0; x < x0+width; x++ {
					i := y*8 + x
					switch {
					case slice.PPS.WeightedBipredIDC == 2 && partition.UseList0 && partition.UseList1:
						chroma[component][i] = implicitBPrediction(chroma0[component][i], chroma1[component][i], slice, references[0][partition.ReferenceIndex], references[1][partition.ReferenceIndexL1])
					case slice.PPS.WeightedBipredIDC == 1:
						chroma[component][i] = weightedBPrediction(chroma0[component][i], chroma1[component][i], partition.UseList0, partition.UseList1,
							bWeight(slice, 0, partition.ReferenceIndex, component), bWeight(slice, 1, partition.ReferenceIndexL1, component), slice.ChromaLog2WeightDenom)
					case partition.UseList0 && partition.UseList1:
						chroma[component][i] = uint8((int(chroma0[component][i]) + int(chroma1[component][i]) + 1) >> 1)
					case partition.UseList0:
						chroma[component][i] = chroma0[component][i]
					default:
						chroma[component][i] = chroma1[component][i]
					}
				}
			}
		}
	}
	for block := range 16 {
		spatial, transformErr := InverseTransform4x4(residual.Blocks[block], qp)
		if transformErr != nil {
			return transformErr
		}
		bx, by := lumaBlockXY(block)
		for y := range 4 {
			for x := range 4 {
				index := (by*4+y)*16 + bx*4 + x
				luma[index] = clipByte(int64(luma[index]) + spatial[y*4+x])
			}
		}
	}
	if err := frame.WriteLumaMacroblock(mbX, mbY, luma); err != nil {
		return err
	}
	chromaQP, _ := ChromaQP420(qp, slice.PPS.ChromaQPIndexOffset)
	spatial, err := TransformChromaResidual420(chromaResidual, [2]int{chromaQP, chromaQP})
	if err != nil {
		return err
	}
	for component := range 2 {
		chroma[component] = ReconstructChroma420(chroma[component], spatial[component])
	}
	return frame.WriteChromaMacroblock(mbX, mbY, chroma[0], chroma[1])
}

type sampleWeight struct{ weight, offset int64 }

func bWeight(slice SliceHeader, list int, reference uint64, component int) sampleWeight {
	if int(reference) >= len(slice.PredictionWeights[list]) {
		denom := slice.LumaLog2WeightDenom
		if component >= 0 {
			denom = slice.ChromaLog2WeightDenom
		}
		return sampleWeight{weight: int64(1) << denom}
	}
	value := slice.PredictionWeights[list][reference]
	if component < 0 {
		return sampleWeight{value.LumaWeight, value.LumaOffset}
	}
	return sampleWeight{value.ChromaWeight[component], value.ChromaOffset[component]}
}

func weightedBPrediction(a, b uint8, useA, useB bool, aWeight, bWeight sampleWeight, denominator uint64) uint8 {
	if useA && useB {
		round := int64(1) << denominator
		offset := (aWeight.offset + bWeight.offset + 1) >> 1
		value := (aWeight.weight*int64(a) + bWeight.weight*int64(b) + round) >> (denominator + 1)
		return clipByte(value + offset)
	}
	round := int64(0)
	if denominator != 0 {
		round = int64(1) << (denominator - 1)
	}
	if useA {
		return clipByte(((aWeight.weight*int64(a) + round) >> denominator) + aWeight.offset)
	}
	return clipByte(((bWeight.weight*int64(b) + round) >> denominator) + bWeight.offset)
}

func implicitBPrediction(a, b uint8, slice SliceHeader, list0, list1 *Frame420) uint8 {
	weight0, weight1 := 32, 32
	if list0 != nil && list1 != nil && !list0.longTerm && !list1.longTerm {
		td := clipInt(int(list1.poc-list0.poc), -128, 127)
		tb := clipInt(int(int64(slice.PictureOrderCountLSB)-list0.poc), -128, 127)
		if td != 0 {
			tx := (16384 + absInt(td/2)) / td
			distanceScale := clipInt((tb*tx+32)>>6, -1024, 1023)
			candidate := distanceScale >> 2
			if candidate >= -64 && candidate <= 128 {
				weight1, weight0 = candidate, 64-candidate
			}
		}
	}
	return clipByte(int64((weight0*int(a) + weight1*int(b) + 32) >> 6))
}

// DecodePSkipFrame decodes a progressive CAVLC P picture consisting entirely
// of P_Skip macroblocks. With no neighbouring non-zero motion vectors, the
// inferred motion vector is (0,0), so each macroblock copies reference list 0.
func DecodePSkipFrame(units []NALUnit, store *ParameterSetStore, reference *Frame420) (*Frame420, error) {
	return DecodePFrame(units, store, []*Frame420{reference})
}

// DecodePFrame decodes a CAVLC P picture against reference list 0.
func DecodePFrame(units []NALUnit, store *ParameterSetStore, references []*Frame420) (*Frame420, error) {
	if len(references) == 0 || references[0] == nil {
		return nil, fmt.Errorf("P picture has no reference frame")
	}
	reference := references[0]
	frame, err := NewFrame420(reference.Width, reference.Height)
	if err != nil {
		return nil, err
	}
	copy(frame.Y, reference.Y)
	copy(frame.Cb, reference.Cb)
	copy(frame.Cr, reference.Cr)
	frame.displayX, frame.displayY = reference.displayX, reference.displayY
	frame.displayWidth, frame.displayHeight = reference.displayWidth, reference.displayHeight
	mbCount := frame.Width / 16 * (frame.Height / 16)
	decoded := make(map[int]bool)
	motion := make(map[[2]int]motionInfo)
	modes := make(map[[2]int]uint8)
	decodedBlocks := make(map[[2]int]bool)
	filter := make(map[int]interDeblockInfo)
	context, _ := NewCAVLCBlockContext(frame.Width / 16)
	sliceID := 0
	for _, unit := range units {
		if unit.Type != NALSliceNonIDR && unit.Type != NALSliceIDR {
			continue
		}
		header, parseErr := ParseSliceHeader(unit, store)
		if parseErr != nil {
			return nil, parseErr
		}
		if header.Type != SliceP || header.PPS.EntropyCodingCABAC || !header.SPS.FrameMbsOnly ||
			header.SPS.ChromaFormat != 1 || header.SPS.BitDepthLuma != 8 || header.SPS.BitDepthChroma != 8 {
			return nil, fmt.Errorf("unsupported P picture: requires progressive 8-bit 4:2:0 CAVLC P-slice")
		}
		r, readErr := SliceDataReader(unit, header)
		if readErr != nil {
			return nil, readErr
		}
		address := int(header.FirstMacroblock)
		qp := int(header.SliceQP)
		for r.MoreRBSPData() {
			run, runErr := r.ReadUE()
			if runErr != nil {
				return nil, malformed("mb_skip_run is truncated")
			}
			if run > uint64(mbCount-address) {
				return nil, malformed("mb_skip_run exceeds picture")
			}
			for range int(run) {
				if address < 0 || address >= mbCount || decoded[address] {
					return nil, malformed("P_Skip macroblock address is invalid or duplicated")
				}
				x, y := address%(frame.Width/16), address/(frame.Width/16)
				mv := predictSkipMotionVector(motion, x*4, y*4)
				partitions := []resolvedInterPartition{{InterPartition: InterPartition{Width: 16, Height: 16}, Motion: mv, Reference: references[0]}}
				if err = reconstructInterMacroblock(frame, references, context, x, y, qp, header, MacroblockHeader{Kind: MacroblockInter}, partitions, r, false); err != nil {
					return nil, fmt.Errorf("P_Skip macroblock %d: %w", address, err)
				}
				recordMotionPartitions(motion, x, y, partitions)
				markInterBlocks(decodedBlocks, x, y, !header.PPS.ConstrainedIntra)
				filter[address] = captureInterDeblock(context, x, y, pDeblockParameters(qp, header, sliceID), false)
				decoded[address] = true
				address++
			}
			if !r.MoreRBSPData() {
				continue
			}
			if address < 0 || address >= mbCount || decoded[address] {
				return nil, malformed("coded P macroblock address is invalid or duplicated")
			}
			x, y := address%(frame.Width/16), address/(frame.Width/16)
			h, headerErr := ParseMacroblockHeader(r, header)
			if headerErr != nil {
				return nil, fmt.Errorf("P macroblock %d: %w", address, headerErr)
			}
			qp = (qp + int(h.QPDelta) + 52) % 52
			if h.Kind != MacroblockInter {
				if header.PPS.ConstrainedIntra {
					return nil, fmt.Errorf("constrained intra prediction inside P pictures is not implemented")
				}
				if err = reconstructPIntraMacroblock(r, header, frame, context, modes, decodedBlocks, x, y, qp, h); err != nil {
					return nil, fmt.Errorf("P intra macroblock %d: %w", address, err)
				}
				markIntraMotion(motion, x, y)
				filter[address] = captureInterDeblock(context, x, y, pDeblockParameters(qp, header, sliceID), true)
				decoded[address] = true
				address++
				continue
			}
			partitions := make([]resolvedInterPartition, len(h.InterPartitions))
			for index, partition := range h.InterPartitions {
				if partition.ReferenceIndex >= uint64(len(references)) || references[partition.ReferenceIndex] == nil {
					return nil, fmt.Errorf("P macroblock %d references unavailable list-0 index %d", address, partition.ReferenceIndex)
				}
				predictor := predictPartitionMotion(motion, x, y, h.RawType, index, partition)
				partitions[index] = resolvedInterPartition{InterPartition: partition, Motion: [2]int{
					predictor[0] + int(partition.MotionDifference[0]), predictor[1] + int(partition.MotionDifference[1]),
				}, Reference: references[partition.ReferenceIndex]}
				recordMotionPartitions(motion, x, y, partitions[index:index+1])
			}
			if err = reconstructInterMacroblock(frame, references, context, x, y, qp, header, h, partitions, r, true); err != nil {
				return nil, fmt.Errorf("P macroblock %d: %w", address, err)
			}
			markInterBlocks(decodedBlocks, x, y, !header.PPS.ConstrainedIntra)
			filter[address] = captureInterDeblock(context, x, y, pDeblockParameters(qp, header, sliceID), false)
			decoded[address] = true
			address++
		}
		sliceID++
	}
	if len(decoded) != mbCount {
		return nil, malformed(fmt.Sprintf("P picture contains %d of %d macroblocks", len(decoded), mbCount))
	}
	deblockInterPicture(frame, filter, motion)
	frame.motion[0] = motion
	return frame, nil
}

func pDeblockParameters(qp int, slice SliceHeader, sliceID int) deblockParameters {
	chromaQP, _ := ChromaQP420(qp, slice.PPS.ChromaQPIndexOffset)
	return deblockParameters{
		qp: qp, chromaQP: chromaQP, alphaOffset: int(slice.SliceAlphaOffset), betaOffset: int(slice.SliceBetaOffset),
		disable: slice.DisableDeblockingFilter, slice: sliceID,
	}
}

func reconstructPIntraMacroblock(
	r *BitReader, slice SliceHeader, frame *Frame420, context *CAVLCBlockContext,
	modes map[[2]int]uint8, decodedBlocks map[[2]int]bool, mbX, mbY, qp int, header MacroblockHeader,
) error {
	if header.Kind == MacroblockPCM {
		if err := decodePCMMacroblock(r, frame, context, mbX, mbY); err != nil {
			return err
		}
	} else if header.Kind == MacroblockIntra16x16 {
		residual, err := DecodeIntra16x16LumaResidual(r, header, context, mbX, mbY)
		if err != nil {
			return err
		}
		spatial, err := TransformIntra16x16Luma(residual, qp)
		if err != nil {
			return err
		}
		prediction, err := PredictIntra16x16(header.Intra16x16Prediction, frame.Intra16Neighbours(mbX, mbY))
		if err != nil {
			return err
		}
		if err = frame.WriteLumaMacroblock(mbX, mbY, ReconstructIntra16x16(prediction, spatial)); err != nil {
			return err
		}
	} else if header.Kind == MacroblockIntra4x4 {
		residual, err := DecodeIntra4x4LumaResidual(r, header, context, mbX, mbY)
		if err != nil {
			return err
		}
		if err = reconstructIntra4Macroblock(frame, mbX, mbY, qp, header, residual, modes, decodedBlocks); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("unsupported intra macroblock kind %d", header.Kind)
	}
	for block := range 16 {
		bx, by := lumaBlockXY(block)
		decodedBlocks[[2]int{mbX*4 + bx, mbY*4 + by}] = true
		if header.Kind != MacroblockIntra4x4 {
			modes[[2]int{mbX*4 + bx, mbY*4 + by}] = 2
		}
	}
	if header.Kind == MacroblockPCM {
		return nil
	}
	chromaResidual, err := DecodeChromaResidual420(r, header, context, mbX, mbY)
	if err != nil {
		return err
	}
	chromaQP, _ := ChromaQP420(qp, slice.PPS.ChromaQPIndexOffset)
	spatial, err := TransformChromaResidual420(chromaResidual, [2]int{chromaQP, chromaQP})
	if err != nil {
		return err
	}
	var reconstructed [2][64]uint8
	for component := range 2 {
		prediction, predictErr := PredictChroma420(header.IntraChromaPrediction, frame.ChromaNeighbours(mbX, mbY, component))
		if predictErr != nil {
			return predictErr
		}
		reconstructed[component] = ReconstructChroma420(prediction, spatial[component])
	}
	return frame.WriteChromaMacroblock(mbX, mbY, reconstructed[0], reconstructed[1])
}

func markInterBlocks(decoded map[[2]int]bool, mbX, mbY int, available bool) {
	if !available {
		return
	}
	for y := range 4 {
		for x := range 4 {
			decoded[[2]int{mbX*4 + x, mbY*4 + y}] = true
		}
	}
}

func markNon4x4IntraModes(modes map[[2]int]uint8, mbX, mbY int) {
	for y := range 4 {
		for x := range 4 {
			modes[[2]int{mbX*4 + x, mbY*4 + y}] = 2
		}
	}
}

func markIntraMotion(field map[[2]int]motionInfo, mbX, mbY int) {
	for y := range 4 {
		for x := range 4 {
			field[[2]int{mbX*4 + x, mbY*4 + y}] = motionInfo{reference: ^uint64(0), intra: true}
		}
	}
}

type motionInfo struct {
	vector    [2]int
	reference uint64
	picture   *Frame420
	intra     bool
	direct    bool
}

type resolvedInterPartition struct {
	InterPartition
	Motion    [2]int
	Reference *Frame420
}

func predictSkipMotionVector(vectors map[[2]int]motionInfo, x, y int) [2]int {
	a, hasA := vectors[[2]int{x - 1, y}]
	b, hasB := vectors[[2]int{x, y - 1}]
	if !hasA || !hasB || a.reference != 0 || b.reference != 0 || a.vector == [2]int{} || b.vector == [2]int{} {
		return [2]int{}
	}
	return predictMotionVector(vectors, x, y, 4, 0)
}

func predictPartitionMotion(vectors map[[2]int]motionInfo, mbX, mbY int, rawType uint64, partitionIndex int, partition InterPartition) [2]int {
	x, y := mbX*4+partition.X/4, mbY*4+partition.Y/4
	a, hasA := vectors[[2]int{x - 1, y}]
	b, hasB := vectors[[2]int{x, y - 1}]
	c, hasC := vectors[[2]int{x + partition.Width/4, y - 1}]
	if !hasC {
		c, hasC = vectors[[2]int{x - 1, y - 1}]
	}
	if rawType == 1 {
		if partitionIndex == 0 && hasB && b.reference == partition.ReferenceIndex {
			return b.vector
		}
		if partitionIndex == 1 && hasA && a.reference == partition.ReferenceIndex {
			return a.vector
		}
	}
	if rawType == 2 {
		if partitionIndex == 0 && hasA && a.reference == partition.ReferenceIndex {
			return a.vector
		}
		if partitionIndex == 1 && hasC && c.reference == partition.ReferenceIndex {
			return c.vector
		}
	}
	return predictMotionVector(vectors, x, y, partition.Width/4, partition.ReferenceIndex)
}

func predictMotionVector(vectors map[[2]int]motionInfo, x, y, width int, reference uint64) [2]int {
	a, hasA := vectors[[2]int{x - 1, y}]
	b, hasB := vectors[[2]int{x, y - 1}]
	c, hasC := vectors[[2]int{x + width, y - 1}]
	if !hasC {
		c, hasC = vectors[[2]int{x - 1, y - 1}]
	}
	matching := make([][2]int, 0, 3)
	for _, item := range []struct {
		value motionInfo
		ok    bool
	}{{a, hasA}, {b, hasB}, {c, hasC}} {
		if item.ok && item.value.reference == reference {
			matching = append(matching, item.value.vector)
		}
	}
	if len(matching) == 1 {
		return matching[0]
	}
	available := 0
	var only [2]int
	for _, item := range []struct {
		value motionInfo
		ok    bool
	}{{a, hasA}, {b, hasB}, {c, hasC}} {
		if item.ok {
			available++
			only = item.value.vector
		}
	}
	if available == 0 {
		return [2]int{}
	}
	if available == 1 {
		return only
	}
	return [2]int{median3(a.vector[0], b.vector[0], c.vector[0]), median3(a.vector[1], b.vector[1], c.vector[1])}
}

func recordMotionPartitions(field map[[2]int]motionInfo, mbX, mbY int, partitions []resolvedInterPartition) {
	for _, partition := range partitions {
		for y := partition.Y / 4; y < (partition.Y+partition.Height)/4; y++ {
			for x := partition.X / 4; x < (partition.X+partition.Width)/4; x++ {
				field[[2]int{mbX*4 + x, mbY*4 + y}] = motionInfo{
					vector: partition.Motion, reference: partition.ReferenceIndex,
					picture: partition.Reference, direct: partition.Direct,
				}
			}
		}
	}
}

func median3(a, b, c int) int {
	if a > b {
		a, b = b, a
	}
	if b > c {
		b, c = c, b
	}
	if a > b {
		b = a
	}
	return b
}

func reconstructInterMacroblock(
	frame *Frame420, references []*Frame420, context *CAVLCBlockContext, mbX, mbY, qp int, slice SliceHeader,
	header MacroblockHeader, partitions []resolvedInterPartition, r *BitReader, coded bool,
) error {
	var lumaResidual Intra4x4LumaResidual
	var err error
	if coded {
		lumaResidual, err = DecodeIntra4x4LumaResidual(r, header, context, mbX, mbY)
	} else {
		for block := range 16 {
			if err = context.setTotalCoeff(mbX, mbY, block, 0); err != nil {
				return err
			}
		}
	}
	if err != nil {
		return err
	}
	chromaResidual, err := DecodeChromaResidual420(r, header, context, mbX, mbY)
	if err != nil {
		return err
	}
	return reconstructInterMacroblockResidual(frame, references, mbX, mbY, qp, slice, partitions, lumaResidual, chromaResidual)
}

func reconstructInterMacroblockResidual(
	frame *Frame420, references []*Frame420, mbX, mbY, qp int, slice SliceHeader,
	partitions []resolvedInterPartition, lumaResidual Intra4x4LumaResidual, chromaResidual ChromaResidual420,
) error {
	var luma [256]uint8
	var chroma [2][64]uint8
	for _, partition := range partitions {
		reference := references[partition.ReferenceIndex]
		predictInterLumaPartition(&luma, reference, mbX, mbY, partition)
		predictInterChromaPartition(&chroma, reference, mbX, mbY, partition)
		if slice.PPS.WeightedPrediction {
			applyWeightedPartition(&luma, &chroma, partition, slice)
		}
	}
	for block := range 16 {
		spatial, transformErr := InverseTransform4x4(lumaResidual.Blocks[block], qp)
		if transformErr != nil {
			return transformErr
		}
		bx, by := lumaBlockXY(block)
		for y := range 4 {
			for x := range 4 {
				position := (by*4+y)*16 + bx*4 + x
				luma[position] = clipByte(int64(luma[position]) + spatial[y*4+x])
			}
		}
	}
	if err := frame.WriteLumaMacroblock(mbX, mbY, luma); err != nil {
		return err
	}
	chromaQP, _ := ChromaQP420(qp, slice.PPS.ChromaQPIndexOffset)
	spatial, err := TransformChromaResidual420(chromaResidual, [2]int{chromaQP, chromaQP})
	if err != nil {
		return err
	}
	for component := range 2 {
		chroma[component] = ReconstructChroma420(chroma[component], spatial[component])
	}
	return frame.WriteChromaMacroblock(mbX, mbY, chroma[0], chroma[1])
}

func applyWeightedPartition(luma *[256]uint8, chroma *[2][64]uint8, partition resolvedInterPartition, slice SliceHeader) {
	if int(partition.ReferenceIndex) >= len(slice.PredictionWeights[0]) {
		return
	}
	weight := slice.PredictionWeights[0][partition.ReferenceIndex]
	lumaRound := int64(0)
	if slice.LumaLog2WeightDenom != 0 {
		lumaRound = int64(1) << (slice.LumaLog2WeightDenom - 1)
	}
	for y := partition.Y; y < partition.Y+partition.Height; y++ {
		for x := partition.X; x < partition.X+partition.Width; x++ {
			index := y*16 + x
			value := (weight.LumaWeight*int64(luma[index]) + lumaRound) >> slice.LumaLog2WeightDenom
			luma[index] = clipByte(value + weight.LumaOffset)
		}
	}
	chromaRound := int64(0)
	if slice.ChromaLog2WeightDenom != 0 {
		chromaRound = int64(1) << (slice.ChromaLog2WeightDenom - 1)
	}
	x0, y0, width, height := partition.X/2, partition.Y/2, partition.Width/2, partition.Height/2
	for component := range 2 {
		for y := y0; y < y0+height; y++ {
			for x := x0; x < x0+width; x++ {
				index := y*8 + x
				value := (weight.ChromaWeight[component]*int64(chroma[component][index]) + chromaRound) >> slice.ChromaLog2WeightDenom
				chroma[component][index] = clipByte(value + weight.ChromaOffset[component])
			}
		}
	}
}

func predictInterLuma(reference *Frame420, mbX, mbY int, mv [2]int) [256]uint8 {
	var result [256]uint8
	baseX, fracX := floorDivMod(mv[0], 4)
	baseY, fracY := floorDivMod(mv[1], 4)
	x0, y0 := mbX*16+baseX, mbY*16+baseY
	for y := range 16 {
		for x := range 16 {
			result[y*16+x] = lumaQuarterSample(reference, x0+x, y0+y, fracX, fracY)
		}
	}
	return result
}

func predictInterLumaPartition(destination *[256]uint8, reference *Frame420, mbX, mbY int, partition resolvedInterPartition) {
	prediction := predictInterLuma(reference, mbX, mbY, partition.Motion)
	for y := partition.Y; y < partition.Y+partition.Height; y++ {
		copy(destination[y*16+partition.X:y*16+partition.X+partition.Width], prediction[y*16+partition.X:y*16+partition.X+partition.Width])
	}
}

func lumaQuarterSample(frame *Frame420, x, y, fracX, fracY int) uint8 {
	integer := func(px, py int) uint8 {
		px, py = clamp(px, 0, frame.Width-1), clamp(py, 0, frame.Height-1)
		return frame.Y[py*frame.Width+px]
	}
	average := func(a, b uint8) uint8 { return uint8((int(a) + int(b) + 1) >> 1) }
	if fracX == 0 && fracY == 0 {
		return integer(x, y)
	}
	horizontal := func(px, py int) uint8 { return lumaHalfHorizontal(frame, px, py) }
	vertical := func(px, py int) uint8 { return lumaHalfVertical(frame, px, py) }
	diagonal := func(px, py int) uint8 { return lumaHalfDiagonal(frame, px, py) }
	switch {
	case fracY == 0:
		half := horizontal(x, y)
		if fracX == 2 {
			return half
		}
		if fracX == 1 {
			return average(integer(x, y), half)
		}
		return average(half, integer(x+1, y))
	case fracX == 0:
		half := vertical(x, y)
		if fracY == 2 {
			return half
		}
		if fracY == 1 {
			return average(integer(x, y), half)
		}
		return average(half, integer(x, y+1))
	case fracX == 2:
		diag := diagonal(x, y)
		if fracY == 2 {
			return diag
		}
		if fracY == 1 {
			return average(horizontal(x, y), diag)
		}
		return average(diag, horizontal(x, y+1))
	case fracY == 2:
		diag := diagonal(x, y)
		if fracX == 1 {
			return average(vertical(x, y), diag)
		}
		return average(diag, vertical(x+1, y))
	case fracX == 1 && fracY == 1:
		return average(horizontal(x, y), vertical(x, y))
	case fracX == 3 && fracY == 1:
		return average(horizontal(x, y), vertical(x+1, y))
	case fracX == 1 && fracY == 3:
		return average(horizontal(x, y+1), vertical(x, y))
	default: // fracX == 3 && fracY == 3
		return average(horizontal(x, y+1), vertical(x+1, y))
	}
}

var interpolationWeights = [6]int{1, -5, 20, 20, -5, 1}

func lumaHalfHorizontal(frame *Frame420, x, y int) uint8 {
	sum := 0
	y = clamp(y, 0, frame.Height-1)
	for tap, weight := range interpolationWeights {
		sx := clamp(x+tap-2, 0, frame.Width-1)
		sum += weight * int(frame.Y[y*frame.Width+sx])
	}
	return clipByte(int64((sum + 16) >> 5))
}

func lumaHalfVertical(frame *Frame420, x, y int) uint8 {
	sum := 0
	x = clamp(x, 0, frame.Width-1)
	for tap, weight := range interpolationWeights {
		sy := clamp(y+tap-2, 0, frame.Height-1)
		sum += weight * int(frame.Y[sy*frame.Width+x])
	}
	return clipByte(int64((sum + 16) >> 5))
}

func lumaHalfDiagonal(frame *Frame420, x, y int) uint8 {
	sum := 0
	for verticalTap, verticalWeight := range interpolationWeights {
		sy := clamp(y+verticalTap-2, 0, frame.Height-1)
		horizontal := 0
		for horizontalTap, horizontalWeight := range interpolationWeights {
			sx := clamp(x+horizontalTap-2, 0, frame.Width-1)
			horizontal += horizontalWeight * int(frame.Y[sy*frame.Width+sx])
		}
		sum += verticalWeight * horizontal
	}
	return clipByte(int64((sum + 512) >> 10))
}

func predictInterChroma(reference *Frame420, mbX, mbY int, mv [2]int) [2][64]uint8 {
	var result [2][64]uint8
	baseX, fracX := floorDivMod(mv[0], 8)
	baseY, fracY := floorDivMod(mv[1], 8)
	width, height := reference.Width/2, reference.Height/2
	for component, plane := range [][]uint8{reference.Cb, reference.Cr} {
		for y := range 8 {
			for x := range 8 {
				sx, sy := mbX*8+x+baseX, mbY*8+y+baseY
				x0, x1 := clamp(sx, 0, width-1), clamp(sx+1, 0, width-1)
				y0, y1 := clamp(sy, 0, height-1), clamp(sy+1, 0, height-1)
				a, b := int(plane[y0*width+x0]), int(plane[y0*width+x1])
				c, d := int(plane[y1*width+x0]), int(plane[y1*width+x1])
				result[component][y*8+x] = uint8(((8-fracX)*(8-fracY)*a + fracX*(8-fracY)*b + (8-fracX)*fracY*c + fracX*fracY*d + 32) >> 6)
			}
		}
	}
	return result
}

func predictInterChromaPartition(destination *[2][64]uint8, reference *Frame420, mbX, mbY int, partition resolvedInterPartition) {
	prediction := predictInterChroma(reference, mbX, mbY, partition.Motion)
	x0, y0, width, height := partition.X/2, partition.Y/2, partition.Width/2, partition.Height/2
	for component := range 2 {
		for y := y0; y < y0+height; y++ {
			copy(destination[component][y*8+x0:y*8+x0+width], prediction[component][y*8+x0:y*8+x0+width])
		}
	}
}

func floorDivMod(value, divisor int) (quotient, remainder int) {
	quotient, remainder = value/divisor, value%divisor
	if remainder < 0 {
		quotient--
		remainder += divisor
	}
	return
}

func clamp(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

// DecodeIDRFrame decodes progressive 8-bit 4:2:0 CAVLC intra slices into one
// frame. Inter prediction and CABAC are handled by later decoder stages.
func DecodeIDRFrame(units []NALUnit, store *ParameterSetStore) (*Frame420, error) {
	var frame *Frame420
	var context *CAVLCBlockContext
	modes := make(map[[2]int]uint8)
	decodedBlocks := make(map[[2]int]bool)
	decodedMacroblocks := make(map[int]bool)
	filter := make(map[int]deblockParameters)
	sliceID := 0
	for _, unit := range units {
		if unit.Type != NALSliceIDR && unit.Type != NALSliceNonIDR {
			continue
		}
		header, err := ParseSliceHeader(unit, store)
		if err != nil {
			return nil, err
		}
		if header.Type != SliceI || header.PPS.EntropyCodingCABAC || !header.SPS.FrameMbsOnly ||
			header.SPS.ChromaFormat != 1 || header.SPS.BitDepthLuma != 8 || header.SPS.BitDepthChroma != 8 {
			return nil, fmt.Errorf("unsupported slice profile: requires progressive 8-bit 4:2:0 CAVLC I-slice")
		}
		width, height := int(header.SPS.CodedWidth), int(header.SPS.CodedHeight)
		if frame == nil {
			frame, err = NewFrame420(width, height)
			if err != nil {
				return nil, err
			}
			context, _ = NewCAVLCBlockContext(width / 16)
			cropUnitX, cropUnitY := cropUnits(header.SPS.ChromaFormat, header.SPS.SeparateColourPlane, 1)
			if err = frame.setDisplayCrop(
				int(header.SPS.CropLeft*cropUnitX), int(header.SPS.CropTop*cropUnitY),
				int(header.SPS.Width), int(header.SPS.Height),
			); err != nil {
				return nil, err
			}
		} else if frame.Width != width || frame.Height != height {
			return nil, malformed("slices use different frame dimensions")
		}
		reader, err := SliceDataReader(unit, header)
		if err != nil {
			return nil, err
		}
		if err := decodeIntraSlice(reader, header, frame, context, modes, decodedBlocks, decodedMacroblocks, filter, sliceID); err != nil {
			return nil, err
		}
		sliceID++
	}
	if frame == nil {
		return nil, fmt.Errorf("no coded slice NAL units")
	}
	want := frame.Width / 16 * (frame.Height / 16)
	if len(decodedMacroblocks) != want {
		return nil, malformed(fmt.Sprintf("picture contains %d of %d macroblocks", len(decodedMacroblocks), want))
	}
	deblockIntraPicture(frame, filter)
	return frame, nil
}

func decodeIntraSlice(
	r *BitReader, slice SliceHeader, frame *Frame420, context *CAVLCBlockContext,
	modes map[[2]int]uint8, decodedBlocks map[[2]int]bool, decodedMacroblocks map[int]bool,
	filter map[int]deblockParameters, sliceID int,
) error {
	mbWidth := frame.Width / 16
	address := int(slice.FirstMacroblock)
	qp := int(slice.SliceQP)
	for r.MoreRBSPData() {
		if address < 0 || address >= mbWidth*(frame.Height/16) || decodedMacroblocks[address] {
			return malformed("macroblock address is invalid or duplicated")
		}
		mbX, mbY := address%mbWidth, address/mbWidth
		header, err := ParseMacroblockHeader(r, slice)
		if err != nil {
			return fmt.Errorf("macroblock %d: %w", address, err)
		}
		qp = (qp + int(header.QPDelta) + 52) % 52
		if header.Kind == MacroblockPCM {
			if err = decodePCMMacroblock(r, frame, context, mbX, mbY); err != nil {
				return fmt.Errorf("macroblock %d: %w", address, err)
			}
			for block := range 16 {
				bx, by := lumaBlockXY(block)
				decodedBlocks[[2]int{mbX*4 + bx, mbY*4 + by}] = true
				modes[[2]int{mbX*4 + bx, mbY*4 + by}] = 2
			}
			chromaQP, _ := ChromaQP420(qp, slice.PPS.ChromaQPIndexOffset)
			filter[address] = deblockParameters{
				qp: qp, chromaQP: chromaQP, alphaOffset: int(slice.SliceAlphaOffset), betaOffset: int(slice.SliceBetaOffset),
				disable: slice.DisableDeblockingFilter, slice: sliceID,
			}
			decodedMacroblocks[address] = true
			address++
			continue
		} else if header.Kind == MacroblockIntra16x16 {
			residual, decodeErr := DecodeIntra16x16LumaResidual(r, header, context, mbX, mbY)
			if decodeErr != nil {
				return decodeErr
			}
			spatial, transformErr := TransformIntra16x16Luma(residual, qp)
			if transformErr != nil {
				return transformErr
			}
			prediction, predictErr := PredictIntra16x16(header.Intra16x16Prediction, frame.Intra16Neighbours(mbX, mbY))
			if predictErr != nil {
				return predictErr
			}
			if err = frame.WriteLumaMacroblock(mbX, mbY, ReconstructIntra16x16(prediction, spatial)); err != nil {
				return err
			}
			for block := range 16 {
				bx, by := lumaBlockXY(block)
				decodedBlocks[[2]int{mbX*4 + bx, mbY*4 + by}] = true
				modes[[2]int{mbX*4 + bx, mbY*4 + by}] = 2
			}
		} else if header.Kind == MacroblockIntra4x4 {
			residual, decodeErr := DecodeIntra4x4LumaResidual(r, header, context, mbX, mbY)
			if decodeErr != nil {
				return decodeErr
			}
			if err = reconstructIntra4Macroblock(frame, mbX, mbY, qp, header, residual, modes, decodedBlocks); err != nil {
				return err
			}
		}
		chromaResidual, err := DecodeChromaResidual420(r, header, context, mbX, mbY)
		if err != nil {
			return fmt.Errorf("macroblock %d type=%d kind=%d cbp=(%d,%d) qp=%d chroma residual: %w",
				address, header.RawType, header.Kind, header.CodedBlockPatternLuma,
				header.CodedBlockPatternChroma, qp, err)
		}
		chromaQP, _ := ChromaQP420(qp, slice.PPS.ChromaQPIndexOffset)
		chromaSpatial, err := TransformChromaResidual420(chromaResidual, [2]int{chromaQP, chromaQP})
		if err != nil {
			return err
		}
		var reconstructed [2][64]uint8
		for component := range 2 {
			prediction, predictErr := PredictChroma420(header.IntraChromaPrediction, frame.ChromaNeighbours(mbX, mbY, component))
			if predictErr != nil {
				return predictErr
			}
			reconstructed[component] = ReconstructChroma420(prediction, chromaSpatial[component])
		}
		if err = frame.WriteChromaMacroblock(mbX, mbY, reconstructed[0], reconstructed[1]); err != nil {
			return err
		}
		decodedMacroblocks[address] = true
		filterChromaQP, _ := ChromaQP420(qp, slice.PPS.ChromaQPIndexOffset)
		filter[address] = deblockParameters{
			qp: qp, chromaQP: filterChromaQP, alphaOffset: int(slice.SliceAlphaOffset), betaOffset: int(slice.SliceBetaOffset),
			disable: slice.DisableDeblockingFilter, slice: sliceID,
		}
		address++
	}
	return nil
}

func decodePCMMacroblock(r *BitReader, frame *Frame420, context *CAVLCBlockContext, mbX, mbY int) error {
	if err := r.AlignToByte(); err != nil {
		return err
	}
	var luma [256]uint8
	var chroma [2][64]uint8
	for i := range luma {
		value, err := r.ReadByte()
		if err != nil {
			return malformed("I_PCM luma samples are truncated")
		}
		luma[i] = value
	}
	for component := range chroma {
		for i := range chroma[component] {
			value, err := r.ReadByte()
			if err != nil {
				return malformed("I_PCM chroma samples are truncated")
			}
			chroma[component][i] = value
		}
	}
	if err := frame.WriteLumaMacroblock(mbX, mbY, luma); err != nil {
		return err
	}
	if err := frame.WriteChromaMacroblock(mbX, mbY, chroma[0], chroma[1]); err != nil {
		return err
	}
	return context.setPCM(mbX, mbY)
}

func reconstructIntra4Macroblock(
	frame *Frame420, mbX, mbY, qp int, header MacroblockHeader, residual Intra4x4LumaResidual,
	modes map[[2]int]uint8, decoded map[[2]int]bool,
) error {
	for block := range 16 {
		bx, by := lumaBlockXY(block)
		globalBlock := [2]int{mbX*4 + bx, mbY*4 + by}
		modeA, hasA := modes[[2]int{globalBlock[0] - 1, globalBlock[1]}]
		modeB, hasB := modes[[2]int{globalBlock[0], globalBlock[1] - 1}]
		predicted := uint8(2)
		if hasA && hasB {
			predicted = modeA
			if modeB < predicted {
				predicted = modeB
			}
		}
		syntax := header.Intra4x4Prediction[block]
		mode := predicted
		if !syntax.Previous {
			mode = syntax.Rem
			if mode >= predicted {
				mode++
			}
		}
		neighbours := frame.intra4Neighbours(globalBlock[0], globalBlock[1], decoded)
		prediction, err := PredictIntra4x4(mode, neighbours)
		if err != nil {
			return fmt.Errorf("Intra4x4 block %d at (%d,%d), mode=%d predicted=%d: %w", block, globalBlock[0], globalBlock[1], mode, predicted, err)
		}
		spatial, err := InverseTransform4x4(residual.Blocks[block], qp)
		if err != nil {
			return err
		}
		x0, y0 := globalBlock[0]*4, globalBlock[1]*4
		for y := range 4 {
			for x := range 4 {
				frame.Y[(y0+y)*frame.Width+x0+x] = clipByte(int64(prediction[y*4+x]) + spatial[y*4+x])
			}
		}
		modes[globalBlock], decoded[globalBlock] = mode, true
	}
	return nil
}

func (f *Frame420) intra4Neighbours(blockX, blockY int, decoded map[[2]int]bool) Intra4Neighbours {
	x0, y0 := blockX*4, blockY*4
	var n Intra4Neighbours
	if decoded[[2]int{blockX, blockY - 1}] {
		n.TopAvailable = true
		for x := range 4 {
			n.Top[x] = f.Y[(y0-1)*f.Width+x0+x]
		}
		if decoded[[2]int{blockX + 1, blockY - 1}] {
			n.TopRightAvailable = true
			for x := 4; x < 8; x++ {
				n.Top[x] = f.Y[(y0-1)*f.Width+x0+x]
			}
		} else {
			n.TopRightAvailable = true
			for x := 4; x < 8; x++ {
				n.Top[x] = n.Top[3]
			}
		}
	}
	if decoded[[2]int{blockX - 1, blockY}] {
		n.LeftAvailable = true
		for y := range 4 {
			n.Left[y] = f.Y[(y0+y)*f.Width+x0-1]
		}
	}
	if decoded[[2]int{blockX - 1, blockY - 1}] {
		n.TopLeftAvailable = true
		n.TopLeft = f.Y[(y0-1)*f.Width+x0-1]
	}
	return n
}
