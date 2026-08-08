package h264

import "fmt"

// DecodeCABACIFrame decodes progressive 8-bit 4:2:0 CABAC I pictures containing
// any mixture of I_4x4, I_16x16 and I_PCM macroblocks.
func DecodeCABACIFrame(units []NALUnit, store *ParameterSetStore) (*Frame420, error) {
	var frame *Frame420
	var context *CAVLCBlockContext
	var residualContext *CABACResidualContext
	decoded := make(map[int]uint64)
	type decodedHeader struct {
		header  MacroblockHeader
		sliceID int
	}
	headers := make(map[int]decodedHeader)
	modes := make(map[[2]int]uint8)
	decodedBlocks := make(map[[2]int]bool)
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
		if header.Type != SliceI || !header.PPS.EntropyCodingCABAC || !header.SPS.FrameMbsOnly ||
			header.SPS.ChromaFormat != 1 || header.SPS.BitDepthLuma != 8 || header.SPS.BitDepthChroma != 8 {
			return nil, fmt.Errorf("unsupported CABAC I picture profile")
		}
		if frame == nil {
			frame, err = NewFrame420(int(header.SPS.CodedWidth), int(header.SPS.CodedHeight))
			if err != nil {
				return nil, err
			}
			context, _ = NewCAVLCBlockContext(frame.Width / 16)
			residualContext, _ = NewCABACResidualContext(frame.Width / 16)
			cropX, cropY := cropUnits(header.SPS.ChromaFormat, header.SPS.SeparateColourPlane, 1)
			if err = frame.setDisplayCrop(int(header.SPS.CropLeft*cropX), int(header.SPS.CropTop*cropY), int(header.SPS.Width), int(header.SPS.Height)); err != nil {
				return nil, err
			}
		} else if frame.Width != int(header.SPS.CodedWidth) || frame.Height != int(header.SPS.CodedHeight) {
			return nil, malformed("CABAC I slices use different frame dimensions")
		}
		reader, err := SliceDataReader(unit, header)
		if err != nil {
			return nil, err
		}
		decoder, err := NewCABACDecoder(reader)
		if err != nil {
			return nil, err
		}
		models, err := InitializeEarlyCABACModels(SliceI, 0, int(header.SliceQP))
		if err != nil {
			return nil, err
		}
		mbWidth := frame.Width / 16
		address, qp := int(header.FirstMacroblock), int(header.SliceQP)
		previousQPContext := false
		for {
			if _, exists := decoded[address]; address < 0 || address >= mbWidth*(frame.Height/16) || exists {
				return nil, malformed("CABAC I_PCM macroblock address is invalid or duplicated")
			}
			neighbour := func(candidate int, available bool) CABACMacroblockNeighbour {
				if !available {
					return CABACMacroblockNeighbour{}
				}
				value, exists := headers[candidate]
				if !exists || value.sliceID != sliceID {
					return CABACMacroblockNeighbour{}
				}
				return CABACMacroblockNeighbour{Available: true, Header: value.header}
			}
			left := neighbour(address-1, address%mbWidth != 0)
			top := neighbour(address-mbWidth, address >= mbWidth)
			macroblock, err := DecodeCABACIMacroblockHeader(&models, decoder, header, left, top, previousQPContext)
			if err != nil {
				return nil, fmt.Errorf("CABAC macroblock %d: %w", address, err)
			}
			x, y := address%mbWidth, address/mbWidth
			if err = residualContext.BeginMacroblock(x, y, sliceID, macroblock, false); err != nil {
				return nil, err
			}
			if macroblock.Kind != MacroblockPCM {
				qp = (qp + int(macroblock.QPDelta) + 52) % 52
			}
			if macroblock.Kind == MacroblockPCM {
				if err = decodePCMMacroblock(reader, frame, context, x, y); err != nil {
					return nil, err
				}
				for block := range 16 {
					bx, by := lumaBlockXY(block)
					decodedBlocks[[2]int{x*4 + bx, y*4 + by}] = true
				}
				markNon4x4IntraModes(modes, x, y)
			} else {
				if macroblock.Kind == MacroblockIntra16x16 {
					residual, decodeErr := residualContext.DecodeLuma16x16(&models, decoder, macroblock, x, y)
					if decodeErr != nil {
						return nil, decodeErr
					}
					spatial, transformErr := TransformIntra16x16Luma(residual, qp)
					if transformErr != nil {
						return nil, transformErr
					}
					prediction, predictErr := PredictIntra16x16(macroblock.Intra16x16Prediction, frame.Intra16Neighbours(x, y))
					if predictErr != nil {
						return nil, fmt.Errorf("CABAC I macroblock %d type=%d intra16_mode=%d qp=%d: %w",
							address, macroblock.RawType, macroblock.Intra16x16Prediction, qp, predictErr)
					}
					if err = frame.WriteLumaMacroblock(x, y, ReconstructIntra16x16(prediction, spatial)); err != nil {
						return nil, err
					}
					for block := range 16 {
						bx, by := lumaBlockXY(block)
						decodedBlocks[[2]int{x*4 + bx, y*4 + by}] = true
					}
					markNon4x4IntraModes(modes, x, y)
				} else {
					residual, decodeErr := residualContext.DecodeLuma4x4(&models, decoder, macroblock, x, y)
					if decodeErr != nil {
						return nil, decodeErr
					}
					if err = reconstructIntra4Macroblock(frame, x, y, qp, macroblock, residual, modes, decodedBlocks); err != nil {
						return nil, err
					}
				}
				chromaResidual, decodeErr := residualContext.DecodeChroma420(&models, decoder, macroblock, x, y)
				if decodeErr != nil {
					return nil, decodeErr
				}
				chromaQP, _ := ChromaQP420(qp, header.PPS.ChromaQPIndexOffset)
				chromaSpatial, transformErr := TransformChromaResidual420(chromaResidual, [2]int{chromaQP, chromaQP})
				if transformErr != nil {
					return nil, transformErr
				}
				var reconstructed [2][64]uint8
				for component := range 2 {
					prediction, predictErr := PredictChroma420(macroblock.IntraChromaPrediction, frame.ChromaNeighbours(x, y, component))
					if predictErr != nil {
						return nil, predictErr
					}
					reconstructed[component] = ReconstructChroma420(prediction, chromaSpatial[component])
				}
				if err = frame.WriteChromaMacroblock(x, y, reconstructed[0], reconstructed[1]); err != nil {
					return nil, err
				}
			}
			decoded[address] = macroblock.RawType
			headers[address] = decodedHeader{header: macroblock, sliceID: sliceID}
			previousQPContext = macroblock.Kind != MacroblockPCM &&
				(macroblock.Kind == MacroblockIntra16x16 || macroblock.CodedBlockPatternLuma != 0 || macroblock.CodedBlockPatternChroma != 0) &&
				macroblock.QPDelta != 0
			chromaQP, _ := ChromaQP420(qp, header.PPS.ChromaQPIndexOffset)
			filter[address] = deblockParameters{
				qp: qp, chromaQP: chromaQP, alphaOffset: int(header.SliceAlphaOffset), betaOffset: int(header.SliceBetaOffset),
				disable: header.DisableDeblockingFilter, slice: sliceID,
			}
			address++
			if macroblock.Kind == MacroblockPCM {
				decoder, err = NewCABACDecoder(reader)
				if err != nil {
					return nil, fmt.Errorf("CABAC restart after I_PCM: %w", err)
				}
			}
			end, err := decoder.DecodeTerminate()
			if err != nil {
				return nil, err
			}
			if end != 0 {
				break
			}
		}
		sliceID++
	}
	if frame == nil {
		return nil, fmt.Errorf("no CABAC I slice")
	}
	want := frame.Width / 16 * (frame.Height / 16)
	if len(decoded) != want {
		return nil, malformed(fmt.Sprintf("CABAC I picture contains %d of %d macroblocks", len(decoded), want))
	}
	deblockIntraPicture(frame, filter)
	return frame, nil
}

// DecodeCABACIPCMFrame is retained for compatibility with the earlier
// all-I_PCM decoder API.
func DecodeCABACIPCMFrame(units []NALUnit, store *ParameterSetStore) (*Frame420, error) {
	return DecodeCABACIFrame(units, store)
}

// DecodeCABACPSkipFrame decodes CABAC P pictures containing P_Skip,
// P_L0_16x16, P_16x8, P_8x16 and P_8x8. The historical name is retained for compatibility.
func DecodeCABACPSkipFrame(units []NALUnit, store *ParameterSetStore, references []*Frame420) (*Frame420, error) {
	if len(references) == 0 || references[0] == nil {
		return nil, fmt.Errorf("CABAC P picture has no reference")
	}
	frame, _ := NewFrame420(references[0].Width, references[0].Height)
	copy(frame.Y, references[0].Y)
	copy(frame.Cb, references[0].Cb)
	copy(frame.Cr, references[0].Cr)
	frame.displayX, frame.displayY = references[0].displayX, references[0].displayY
	frame.displayWidth, frame.displayHeight = references[0].displayWidth, references[0].displayHeight
	mbWidth, mbCount := frame.Width/16, frame.Width/16*(frame.Height/16)
	decoded := make(map[int]bool)
	notSkipped := make(map[int]bool)
	motion := make(map[[2]int]motionInfo)
	mvdField := make(map[[2]int][2]int)
	headers := make(map[int]struct {
		header  MacroblockHeader
		sliceID int
	})
	context, _ := NewCAVLCBlockContext(mbWidth)
	residualContext, _ := NewCABACResidualContext(mbWidth)
	filter := make(map[int]interDeblockInfo)
	sliceID := 0
	for _, unit := range units {
		if unit.Type != NALSliceNonIDR {
			continue
		}
		header, err := ParseSliceHeader(unit, store)
		if err != nil {
			return nil, err
		}
		if header.Type != SliceP || !header.PPS.EntropyCodingCABAC {
			return nil, fmt.Errorf("expected CABAC P slice")
		}
		reader, err := SliceDataReader(unit, header)
		if err != nil {
			return nil, err
		}
		decoder, err := NewCABACDecoder(reader)
		if err != nil {
			return nil, err
		}
		models, err := InitializeEarlyCABACModels(SliceP, header.CABACInitIDC, int(header.SliceQP))
		if err != nil {
			return nil, err
		}
		address, qp := int(header.FirstMacroblock), int(header.SliceQP)
		previousQPContext := false
		for {
			if address < 0 || address >= mbCount || decoded[address] {
				return nil, malformed("CABAC P macroblock address is invalid or duplicated")
			}
			skip, err := models.Decode(decoder, 11+skipContextIncrement(notSkipped, address, mbWidth))
			if err != nil {
				return nil, err
			}
			x, y := address%mbWidth, address/mbWidth
			macroblock := MacroblockHeader{Kind: MacroblockInter}
			var partitions []resolvedInterPartition
			if skip != 0 {
				if err = residualContext.BeginMacroblock(x, y, sliceID, macroblock, true); err != nil {
					return nil, err
				}
				mv := predictSkipMotionVector(motion, x*4, y*4)
				partitions = []resolvedInterPartition{{InterPartition: InterPartition{Width: 16, Height: 16, UseList0: true}, Motion: mv, Reference: references[0]}}
				if err = reconstructInterMacroblock(frame, references, context, x, y, qp, header, macroblock, partitions, reader, false); err != nil {
					return nil, err
				}
			} else {
				neighbour := func(candidate int, available bool) CABACMacroblockNeighbour {
					if !available {
						return CABACMacroblockNeighbour{}
					}
					value, exists := headers[candidate]
					if !exists || value.sliceID != sliceID {
						return CABACMacroblockNeighbour{}
					}
					return CABACMacroblockNeighbour{Available: true, Header: value.header}
				}
				left := neighbour(address-1, address%mbWidth != 0)
				top := neighbour(address-mbWidth, address >= mbWidth)
				contextForPartition := func(_ int, partition InterPartition) (int, [2][2]int) {
					px, py := x*4+partition.X/4, y*4+partition.Y/4
					positions := [2][2]int{{px - 1, py}, {px, py - 1}}
					var refCount int
					var neighbours [2][2]int
					for side, position := range positions {
						if position[0] < 0 || position[1] < 0 {
							continue
						}
						ownerX, ownerY := position[0]/4, position[1]/4
						if ownerX != x || ownerY != y {
							ownerAddress := ownerY*mbWidth + ownerX
							owner, exists := headers[ownerAddress]
							if !exists || owner.sliceID != sliceID {
								continue
							}
						}
						if value, exists := motion[position]; exists && !value.intra && value.reference > 0 {
							refCount += side + 1
						}
						if value, exists := mvdField[position]; exists {
							neighbours[0][side], neighbours[1][side] = absInt(value[0]), absInt(value[1])
						}
					}
					return refCount, neighbours
				}
				macroblock, err = decodeCABACPMacroblockHeaderWithContexts(&models, decoder, header, left, top, previousQPContext, contextForPartition)
				if err != nil {
					return nil, fmt.Errorf("CABAC P macroblock %d: %w", address, err)
				}
				for _, partition := range macroblock.InterPartitions {
					if int(partition.ReferenceIndex) >= len(references) {
						return nil, fmt.Errorf("%w: CABAC P macroblock %d sliceQP=%d init=%d type=%d ref=%d available=%d partitions=%+v",
							ErrMalformed, address, header.SliceQP, header.CABACInitIDC, macroblock.RawType,
							partition.ReferenceIndex, len(references), macroblock.InterPartitions)
					}
				}
				if err = residualContext.BeginMacroblock(x, y, sliceID, macroblock, false); err != nil {
					return nil, err
				}
				qp = (qp + int(macroblock.QPDelta) + 52) % 52
				for index, partition := range macroblock.InterPartitions {
					predicted := predictPartitionMotion(motion, x, y, macroblock.RawType, index, partition)
					resolved := resolvedInterPartition{InterPartition: partition, Reference: references[partition.ReferenceIndex]}
					resolved.Motion = [2]int{predicted[0] + int(partition.MotionDifference[0]), predicted[1] + int(partition.MotionDifference[1])}
					partitions = append(partitions, resolved)
				}
				lumaResidual, decodeErr := residualContext.DecodeLuma4x4(&models, decoder, macroblock, x, y)
				if decodeErr != nil {
					return nil, fmt.Errorf("CABAC P macroblock %d init=%d type=%d cbp=(%d,%d) qp-delta=%d left=%+v partitions=%+v luma residual: %w",
						address, header.CABACInitIDC, macroblock.RawType, macroblock.CodedBlockPatternLuma,
						macroblock.CodedBlockPatternChroma, macroblock.QPDelta, left, macroblock.InterPartitions, decodeErr)
				}
				chromaResidual, decodeErr := residualContext.DecodeChroma420(&models, decoder, macroblock, x, y)
				if decodeErr != nil {
					return nil, decodeErr
				}
				if err = recordCABACResidualTotals(context, x, y, lumaResidual, chromaResidual); err != nil {
					return nil, err
				}
				if err = reconstructInterMacroblockResidual(frame, references, x, y, qp, header, partitions, lumaResidual, chromaResidual); err != nil {
					return nil, err
				}
				for _, partition := range macroblock.InterPartitions {
					for by := partition.Y / 4; by < (partition.Y+partition.Height)/4; by++ {
						for bx := partition.X / 4; bx < (partition.X+partition.Width)/4; bx++ {
							mvdField[[2]int{x*4 + bx, y*4 + by}] = [2]int{int(partition.MotionDifference[0]), int(partition.MotionDifference[1])}
						}
					}
				}
				notSkipped[address] = true
			}
			recordMotionPartitions(motion, x, y, partitions)
			decoded[address] = true
			headers[address] = struct {
				header  MacroblockHeader
				sliceID int
			}{macroblock, sliceID}
			previousQPContext = skip == 0 && (macroblock.CodedBlockPatternLuma != 0 || macroblock.CodedBlockPatternChroma != 0) && macroblock.QPDelta != 0
			filter[address] = captureInterDeblock(context, x, y, pDeblockParameters(qp, header, sliceID), false)
			address++
			end, err := decoder.DecodeTerminate()
			if err != nil {
				return nil, err
			}
			if end != 0 {
				break
			}
		}
		sliceID++
	}
	if len(decoded) != mbCount {
		return nil, malformed(fmt.Sprintf("CABAC P picture contains %d of %d macroblocks", len(decoded), mbCount))
	}
	deblockInterPicture(frame, filter, motion)
	frame.motion[0] = motion
	return frame, nil
}

// DecodeCABACBSkipFrame decodes CABAC B pictures containing B_Skip, Direct,
// 16x16, 16x8 and 8x16 inter macroblocks. The historical name is retained.
func DecodeCABACBSkipFrame(units []NALUnit, store *ParameterSetStore, references [2][]*Frame420) (*Frame420, error) {
	if len(references[0]) == 0 || len(references[1]) == 0 {
		return nil, fmt.Errorf("CABAC B picture has incomplete references")
	}
	frame, _ := NewFrame420(references[0][0].Width, references[0][0].Height)
	frame.displayX, frame.displayY = references[0][0].displayX, references[0][0].displayY
	frame.displayWidth, frame.displayHeight = references[0][0].displayWidth, references[0][0].displayHeight
	mbWidth, mbCount := frame.Width/16, frame.Width/16*(frame.Height/16)
	decoded := make(map[int]bool)
	notSkipped := make(map[int]bool)
	motion := [2]map[[2]int]motionInfo{make(map[[2]int]motionInfo), make(map[[2]int]motionInfo)}
	mvdField := [2]map[[2]int][2]int{make(map[[2]int][2]int), make(map[[2]int][2]int)}
	type bHeaderRecord struct {
		header  MacroblockHeader
		sliceID int
		skipped bool
	}
	headers := make(map[int]bHeaderRecord)
	context, _ := NewCAVLCBlockContext(mbWidth)
	residualContext, _ := NewCABACResidualContext(mbWidth)
	modes := make(map[[2]int]uint8)
	decodedBlocks := make(map[[2]int]bool)
	filter := make(map[int]interDeblockInfo)
	sliceID := 0
	for _, unit := range units {
		if unit.Type != NALSliceNonIDR {
			continue
		}
		header, err := ParseSliceHeader(unit, store)
		if err != nil {
			return nil, err
		}
		if header.Type != SliceB || !header.PPS.EntropyCodingCABAC {
			return nil, fmt.Errorf("expected CABAC B slice")
		}
		if !header.SPS.FrameMbsOnly || header.SPS.ChromaFormat != 1 || header.SPS.BitDepthLuma != 8 || header.SPS.BitDepthChroma != 8 {
			return nil, fmt.Errorf("unsupported CABAC B picture profile")
		}
		reader, err := SliceDataReader(unit, header)
		if err != nil {
			return nil, err
		}
		decoder, err := NewCABACDecoder(reader)
		if err != nil {
			return nil, err
		}
		models, err := InitializeEarlyCABACModels(SliceB, header.CABACInitIDC, int(header.SliceQP))
		if err != nil {
			return nil, err
		}
		address, qp := int(header.FirstMacroblock), int(header.SliceQP)
		previousQPContext := false
		for {
			if address < 0 || address >= mbCount || decoded[address] {
				return nil, malformed("CABAC B macroblock address is invalid or duplicated")
			}
			skip, err := models.Decode(decoder, 24+skipContextIncrement(notSkipped, address, mbWidth))
			if err != nil {
				return nil, err
			}
			x, y := address%mbWidth, address/mbWidth
			macroblock := MacroblockHeader{Kind: MacroblockInter}
			var partitions []resolvedBPartition
			if skip != 0 {
				if err = residualContext.BeginMacroblock(x, y, sliceID, macroblock, true); err != nil {
					return nil, err
				}
				partitions, err = deriveDirectPartitions(header, references, motion, x, y, InterPartition{Width: 16, Height: 16, Direct: true})
				if err != nil {
					return nil, err
				}
				if err = reconstructBMacroblock(frame, references, context, x, y, qp, header, macroblock, partitions, reader, false); err != nil {
					return nil, err
				}
			} else {
				neighbour := func(candidate int, available bool) CABACMacroblockNeighbour {
					if !available {
						return CABACMacroblockNeighbour{}
					}
					value, exists := headers[candidate]
					if !exists || value.sliceID != sliceID {
						return CABACMacroblockNeighbour{}
					}
					return CABACMacroblockNeighbour{Available: true, Skipped: value.skipped, Header: value.header}
				}
				left := neighbour(address-1, address%mbWidth != 0)
				top := neighbour(address-mbWidth, address >= mbWidth)
				neighbourNotSkipOrDirect := 0
				for _, value := range []CABACMacroblockNeighbour{left, top} {
					if value.Available && !value.Skipped && !(value.Header.Kind == MacroblockInter && value.Header.Direct) {
						neighbourNotSkipOrDirect++
					}
				}
				contextForPartition := func(_ int, list int, partition InterPartition) (int, [2][2]int) {
					px, py := x*4+partition.X/4, y*4+partition.Y/4
					positions := [2][2]int{{px - 1, py}, {px, py - 1}}
					var refCount int
					var neighbours [2][2]int
					for side, position := range positions {
						if position[0] < 0 || position[1] < 0 {
							continue
						}
						ownerX, ownerY := position[0]/4, position[1]/4
						if ownerX != x || ownerY != y {
							owner, exists := headers[ownerY*mbWidth+ownerX]
							if !exists || owner.sliceID != sliceID {
								continue
							}
						}
						if value, exists := motion[list][position]; exists && !value.intra && !value.direct && value.reference > 0 {
							refCount += side + 1
						}
						if value, exists := mvdField[list][position]; exists {
							neighbours[0][side], neighbours[1][side] = absInt(value[0]), absInt(value[1])
						}
					}
					return refCount, neighbours
				}
				macroblock, err = decodeCABACBMacroblockHeaderWithContexts(&models, decoder, header, left, top,
					neighbourNotSkipOrDirect, previousQPContext, contextForPartition)
				if err != nil {
					return nil, fmt.Errorf("CABAC B macroblock %d: %w", address, err)
				}
				if err = residualContext.BeginMacroblock(x, y, sliceID, macroblock, false); err != nil {
					return nil, err
				}
				if macroblock.Kind != MacroblockPCM {
					qp = (qp + int(macroblock.QPDelta) + 52) % 52
				}
				if macroblock.Kind != MacroblockInter {
					if err = reconstructCABACIntraMacroblock(frame, context, residualContext, &models, decoder, reader,
						header, macroblock, x, y, qp, modes, decodedBlocks); err != nil {
						return nil, err
					}
					markIntraMotion(motion[0], x, y)
					markIntraMotion(motion[1], x, y)
				} else {
					for index, partition := range macroblock.InterPartitions {
						if partition.Direct {
							direct, directErr := deriveDirectPartitions(header, references, motion, x, y, partition)
							if directErr != nil {
								return nil, directErr
							}
							partitions = append(partitions, direct...)
							continue
						}
						resolved := resolvedBPartition{InterPartition: partition}
						predictionShape := uint64(0)
						if partition.Height == 8 {
							predictionShape = 1
						} else if partition.Width == 8 {
							predictionShape = 2
						}
						if partition.UseList0 {
							if int(partition.ReferenceIndex) >= len(references[0]) {
								return nil, malformed("CABAC B list-0 reference index is out of range")
							}
							predictor := predictPartitionMotion(motion[0], x, y, predictionShape, index, partition)
							resolved.MotionL0 = [2]int{predictor[0] + int(partition.MotionDifference[0]), predictor[1] + int(partition.MotionDifference[1])}
						}
						if partition.UseList1 {
							if int(partition.ReferenceIndexL1) >= len(references[1]) {
								return nil, malformed("CABAC B list-1 reference index is out of range")
							}
							p1 := partition
							p1.ReferenceIndex = partition.ReferenceIndexL1
							predictor := predictPartitionMotion(motion[1], x, y, predictionShape, index, p1)
							resolved.MotionL1 = [2]int{predictor[0] + int(partition.MotionDifferenceL1[0]), predictor[1] + int(partition.MotionDifferenceL1[1])}
						}
						partitions = append(partitions, resolved)
					}
					remainingBeforeResidual := reader.BitsRemaining()
					lumaResidual, decodeErr := residualContext.DecodeLuma4x4(&models, decoder, macroblock, x, y)
					if decodeErr != nil {
						return nil, fmt.Errorf("CABAC B macroblock %d init=%d type=%d cbp=(%d,%d) qp-delta=%d bits-before-residual=%d partitions=%+v luma residual: %w", address,
							header.CABACInitIDC, macroblock.RawType, macroblock.CodedBlockPatternLuma, macroblock.CodedBlockPatternChroma, macroblock.QPDelta, remainingBeforeResidual, macroblock.InterPartitions, decodeErr)
					}
					chromaResidual, decodeErr := residualContext.DecodeChroma420(&models, decoder, macroblock, x, y)
					if decodeErr != nil {
						return nil, fmt.Errorf("CABAC B macroblock %d type=%d cbp=(%d,%d) chroma residual: %w", address,
							macroblock.RawType, macroblock.CodedBlockPatternLuma, macroblock.CodedBlockPatternChroma, decodeErr)
					}
					if err = recordCABACResidualTotals(context, x, y, lumaResidual, chromaResidual); err != nil {
						return nil, err
					}
					if err = reconstructBMacroblockResidual(frame, references, x, y, qp, header, partitions, lumaResidual, chromaResidual); err != nil {
						return nil, err
					}
					markInterBlocks(decodedBlocks, x, y, !header.PPS.ConstrainedIntra)
					for _, partition := range macroblock.InterPartitions {
						for by := partition.Y / 4; by < (partition.Y+partition.Height)/4; by++ {
							for bx := partition.X / 4; bx < (partition.X+partition.Width)/4; bx++ {
								coordinate := [2]int{x*4 + bx, y*4 + by}
								if partition.UseList0 {
									mvdField[0][coordinate] = [2]int{int(partition.MotionDifference[0]), int(partition.MotionDifference[1])}
								}
								if partition.UseList1 {
									mvdField[1][coordinate] = [2]int{int(partition.MotionDifferenceL1[0]), int(partition.MotionDifferenceL1[1])}
								}
							}
						}
					}
				}
				notSkipped[address] = true
			}
			if macroblock.Kind == MacroblockInter {
				recordBMotion(motion, x, y, partitions, references)
				markInterBlocks(decodedBlocks, x, y, !header.PPS.ConstrainedIntra)
			}
			decoded[address] = true
			headers[address] = bHeaderRecord{header: macroblock, sliceID: sliceID, skipped: skip != 0}
			previousQPContext = skip == 0 && macroblock.Kind != MacroblockPCM &&
				(macroblock.Kind == MacroblockIntra16x16 || macroblock.CodedBlockPatternLuma != 0 || macroblock.CodedBlockPatternChroma != 0) && macroblock.QPDelta != 0
			filter[address] = captureInterDeblock(context, x, y, pDeblockParameters(qp, header, sliceID), macroblock.Kind != MacroblockInter)
			address++
			if macroblock.Kind == MacroblockPCM {
				decoder, err = NewCABACDecoder(reader)
				if err != nil {
					return nil, fmt.Errorf("CABAC restart after B I_PCM: %w", err)
				}
			}
			end, err := decoder.DecodeTerminate()
			if err != nil {
				return nil, err
			}
			if end != 0 {
				break
			}
		}
		sliceID++
	}
	if len(decoded) != mbCount {
		return nil, malformed(fmt.Sprintf("CABAC B picture contains %d of %d macroblocks", len(decoded), mbCount))
	}
	deblockBPicture(frame, filter, motion)
	frame.motion = motion
	return frame, nil
}

func reconstructCABACIntraMacroblock(frame *Frame420, context *CAVLCBlockContext, residualContext *CABACResidualContext,
	models *CABACModels, decoder cabacSyntaxDecoder, reader *BitReader, slice SliceHeader, macroblock MacroblockHeader,
	mbX, mbY, qp int, modes map[[2]int]uint8, decodedBlocks map[[2]int]bool,
) error {
	if macroblock.Kind == MacroblockPCM {
		if err := decodePCMMacroblock(reader, frame, context, mbX, mbY); err != nil {
			return err
		}
		for block := range 16 {
			bx, by := lumaBlockXY(block)
			decodedBlocks[[2]int{mbX*4 + bx, mbY*4 + by}] = true
		}
		markNon4x4IntraModes(modes, mbX, mbY)
		return nil
	}
	var lumaResidual Intra4x4LumaResidual
	if macroblock.Kind == MacroblockIntra16x16 {
		residual, err := residualContext.DecodeLuma16x16(models, decoder, macroblock, mbX, mbY)
		if err != nil {
			return err
		}
		spatial, err := TransformIntra16x16Luma(residual, qp)
		if err != nil {
			return err
		}
		prediction, err := PredictIntra16x16(macroblock.Intra16x16Prediction, frame.Intra16Neighbours(mbX, mbY))
		if err != nil {
			return err
		}
		if err = frame.WriteLumaMacroblock(mbX, mbY, ReconstructIntra16x16(prediction, spatial)); err != nil {
			return err
		}
		for block := range 16 {
			bx, by := lumaBlockXY(block)
			decodedBlocks[[2]int{mbX*4 + bx, mbY*4 + by}] = true
			copy(lumaResidual.Blocks[block][:], residual.AC[block][:])
		}
		markNon4x4IntraModes(modes, mbX, mbY)
	} else {
		residual, err := residualContext.DecodeLuma4x4(models, decoder, macroblock, mbX, mbY)
		if err != nil {
			return err
		}
		lumaResidual = residual
		if err = reconstructIntra4Macroblock(frame, mbX, mbY, qp, macroblock, residual, modes, decodedBlocks); err != nil {
			return err
		}
	}
	chromaResidual, err := residualContext.DecodeChroma420(models, decoder, macroblock, mbX, mbY)
	if err != nil {
		return err
	}
	if err = recordCABACResidualTotals(context, mbX, mbY, lumaResidual, chromaResidual); err != nil {
		return err
	}
	chromaQP, _ := ChromaQP420(qp, slice.PPS.ChromaQPIndexOffset)
	chromaSpatial, err := TransformChromaResidual420(chromaResidual, [2]int{chromaQP, chromaQP})
	if err != nil {
		return err
	}
	var reconstructed [2][64]uint8
	for component := range 2 {
		prediction, predictErr := PredictChroma420(macroblock.IntraChromaPrediction, frame.ChromaNeighbours(mbX, mbY, component))
		if predictErr != nil {
			return predictErr
		}
		reconstructed[component] = ReconstructChroma420(prediction, chromaSpatial[component])
	}
	return frame.WriteChromaMacroblock(mbX, mbY, reconstructed[0], reconstructed[1])
}

func skipContextIncrement(notSkipped map[int]bool, address, mbWidth int) int {
	increment := 0
	if address%mbWidth != 0 && notSkipped[address-1] {
		increment++
	}
	if address >= mbWidth && notSkipped[address-mbWidth] {
		increment++
	}
	return increment
}

func recordCABACResidualTotals(context *CAVLCBlockContext, mbX, mbY int, luma Intra4x4LumaResidual, chroma ChromaResidual420) error {
	for block := range 16 {
		if err := context.setTotalCoeff(mbX, mbY, block, countNonZero(luma.Blocks[block][:])); err != nil {
			return err
		}
	}
	for component := range 2 {
		for block := range 4 {
			if err := context.setChromaTotal(component, mbX, mbY, block, countNonZero(chroma.AC[component][block][:])); err != nil {
				return err
			}
		}
	}
	return nil
}
