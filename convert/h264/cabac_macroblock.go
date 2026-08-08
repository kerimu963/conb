package h264

import "fmt"

type cabacSyntaxDecoder interface {
	cabacTerminateDecoder
	DecodeBypass() (uint8, error)
}

// CABACMacroblockNeighbour contains the syntax needed by CABAC's left/top
// context derivations. Unavailable neighbours keep Available false.
type CABACMacroblockNeighbour struct {
	Available bool
	Skipped   bool
	Header    MacroblockHeader
}

func (n CABACMacroblockNeighbour) nonI4x4() bool {
	return n.Available && n.Header.Kind != MacroblockIntra4x4
}

func (n CABACMacroblockNeighbour) intraChromaNonZero() bool {
	return n.Available && !n.Skipped &&
		(n.Header.Kind == MacroblockIntra4x4 || n.Header.Kind == MacroblockIntra16x16) &&
		n.Header.IntraChromaPrediction != 0
}

func (n CABACMacroblockNeighbour) cbp() CABACCBPNeighbour {
	return CABACCBPNeighbour{
		Available: n.Available,
		PCM:       n.Header.Kind == MacroblockPCM,
		Luma:      n.Header.CodedBlockPatternLuma,
		Chroma:    n.Header.CodedBlockPatternChroma,
	}
}

// DecodeCABACIMacroblockHeader decodes all non-residual macroblock_layer
// syntax used by progressive 4:2:0 I slices.
func DecodeCABACIMacroblockHeader(models *CABACModels, decoder cabacSyntaxDecoder, slice SliceHeader, left, top CABACMacroblockNeighbour, previousQPContext bool) (MacroblockHeader, error) {
	neighbourTypes := 0
	if left.nonI4x4() {
		neighbourTypes++
	}
	if top.nonI4x4() {
		neighbourTypes++
	}
	rawType, err := DecodeCABACIMacroblockType(models, decoder, neighbourTypes)
	if err != nil {
		return MacroblockHeader{}, err
	}
	return decodeCABACIntraHeaderAfterType(models, decoder, slice, left, top, previousQPContext, rawType)
}

func decodeCABACIntraHeaderAfterType(models *CABACModels, decoder cabacSyntaxDecoder, slice SliceHeader,
	left, top CABACMacroblockNeighbour, previousQPContext bool, rawType uint64,
) (MacroblockHeader, error) {
	header := MacroblockHeader{RawType: rawType}
	if rawType == 25 {
		header.Kind = MacroblockPCM
		return header, nil
	}
	if rawType == 0 {
		header.Kind = MacroblockIntra4x4
		for block := range header.Intra4x4Prediction {
			mode, decodeErr := DecodeCABACIntra4x4Mode(models, decoder)
			if decodeErr != nil {
				return MacroblockHeader{}, fmt.Errorf("CABAC Intra4x4 mode %d: %w", block, decodeErr)
			}
			header.Intra4x4Prediction[block] = IntraPredictionMode{Previous: mode.Prev, Rem: mode.Rem}
		}
	} else if rawType <= 24 {
		header.Kind = MacroblockIntra16x16
		index := rawType - 1
		header.Intra16x16Prediction = uint8(index % 4)
		header.CodedBlockPatternChroma = uint8(index/4) % 3
		if index >= 12 {
			header.CodedBlockPatternLuma = 15
		}
	} else {
		return MacroblockHeader{}, malformed("invalid CABAC I mb_type")
	}

	if slice.SPS.ChromaFormat != 0 && !slice.SPS.SeparateColourPlane {
		mode, decodeErr := DecodeCABACIntraChromaMode(models, decoder, left.intraChromaNonZero(), top.intraChromaNonZero())
		if decodeErr != nil {
			return MacroblockHeader{}, decodeErr
		}
		header.IntraChromaPrediction = uint64(mode)
	}
	if header.Kind == MacroblockIntra4x4 {
		luma, chroma, decodeErr := DecodeCABACCodedBlockPattern(models, decoder, left.cbp(), top.cbp(), slice.SPS.ChromaFormat != 0)
		if decodeErr != nil {
			return MacroblockHeader{}, decodeErr
		}
		header.CodedBlockPatternLuma, header.CodedBlockPatternChroma = luma, chroma
	}
	if header.Kind == MacroblockIntra16x16 || header.CodedBlockPatternLuma != 0 || header.CodedBlockPatternChroma != 0 {
		delta, decodeErr := DecodeCABACMBQPDelta(models, decoder, previousQPContext)
		if decodeErr != nil {
			return MacroblockHeader{}, decodeErr
		}
		qpDepthOffset := int(3 * (slice.SPS.BitDepthLuma - 8))
		if delta < -26-qpDepthOffset || delta > 25+qpDepthOffset {
			return MacroblockHeader{}, malformed("CABAC mb_qp_delta is out of range")
		}
		header.QPDelta = int64(delta)
	}
	return header, nil
}

// DecodeCABACP16x16MacroblockHeader decodes the complete non-residual syntax
// of P_L0_16x16. Other P mb_type values are left to the split-partition path.
func DecodeCABACP16x16MacroblockHeader(models *CABACModels, decoder cabacSyntaxDecoder, slice SliceHeader,
	left, top CABACMacroblockNeighbour, refNeighbourCount int, mvdNeighbours [2][2]int, previousQPContext bool,
) (MacroblockHeader, error) {
	var refs [16]int
	var mvd [16][2][2]int
	refs[0] = refNeighbourCount
	mvd[0] = mvdNeighbours
	return DecodeCABACPMacroblockHeader(models, decoder, slice, left, top, refs, mvd, previousQPContext)
}

// DecodeCABACPMacroblockHeader decodes all CABAC inter P macroblock types.
func DecodeCABACPMacroblockHeader(models *CABACModels, decoder cabacSyntaxDecoder, slice SliceHeader,
	left, top CABACMacroblockNeighbour, refNeighbourCounts [16]int, mvdNeighbours [16][2][2]int, previousQPContext bool,
) (MacroblockHeader, error) {
	return decodeCABACPMacroblockHeaderWithContexts(models, decoder, slice, left, top, previousQPContext,
		func(index int, _ InterPartition) (int, [2][2]int) {
			return refNeighbourCounts[index], mvdNeighbours[index]
		})
}

func decodeCABACPMacroblockHeaderWithContexts(models *CABACModels, decoder cabacSyntaxDecoder, slice SliceHeader,
	left, top CABACMacroblockNeighbour, previousQPContext bool,
	contexts func(int, InterPartition) (int, [2][2]int),
) (MacroblockHeader, error) {
	rawType, err := DecodeCABACPMacroblockType(models, decoder)
	if err != nil {
		return MacroblockHeader{}, err
	}
	if rawType > 3 {
		return MacroblockHeader{}, fmt.Errorf("CABAC P mb_type %d requires sub/intra parsing", rawType)
	}
	header := MacroblockHeader{RawType: rawType, Kind: MacroblockInter}
	switch rawType {
	case 0:
		header.InterPartitions = []InterPartition{{Width: 16, Height: 16, UseList0: true}}
	case 1:
		header.InterPartitions = []InterPartition{{Width: 16, Height: 8, UseList0: true}, {Y: 8, Width: 16, Height: 8, UseList0: true}}
	case 2:
		header.InterPartitions = []InterPartition{{Width: 8, Height: 16, UseList0: true}, {X: 8, Width: 8, Height: 16, UseList0: true}}
	case 3:
		for sub := range 4 {
			typeValue, decodeErr := DecodeCABACSubMacroblockType(models, decoder, SliceP)
			if decodeErr != nil {
				return MacroblockHeader{}, fmt.Errorf("CABAC P sub_mb_type %d: %w", sub, decodeErr)
			}
			header.SubMacroblockTypes[sub] = typeValue
			header.InterPartitions = append(header.InterPartitions, pSubPartitions(sub, typeValue)...)
		}
	}
	partitionContext := func(index int) (int, [2][2]int) {
		partition := header.InterPartitions[index]
		ref, mvd := contexts(index, partition)
		for side, coordinate := range [2][2]int{{partition.X - 1, partition.Y}, {partition.X, partition.Y - 1}} {
			if coordinate[0] < 0 || coordinate[1] < 0 {
				continue
			}
			for previous := index - 1; previous >= 0; previous-- {
				candidate := header.InterPartitions[previous]
				if coordinate[0] < candidate.X || coordinate[0] >= candidate.X+candidate.Width ||
					coordinate[1] < candidate.Y || coordinate[1] >= candidate.Y+candidate.Height {
					continue
				}
				if candidate.ReferenceIndex > 0 {
					ref += side + 1
				}
				mvd[0][side] = absInt(int(candidate.MotionDifference[0]))
				mvd[1][side] = absInt(int(candidate.MotionDifference[1]))
				break
			}
		}
		return ref, mvd
	}
	if rawType == 3 {
		offset := 0
		for sub := range 4 {
			count := len(pSubPartitions(sub, header.SubMacroblockTypes[sub]))
			if slice.ReferenceCount[0] > 1 {
				refContext, _ := partitionContext(offset)
				value, decodeErr := DecodeCABACRefIndex(models, decoder, refContext, int(slice.ReferenceCount[0]-1))
				if decodeErr != nil {
					return MacroblockHeader{}, decodeErr
				}
				for index := offset; index < offset+count; index++ {
					header.InterPartitions[index].ReferenceIndex = uint64(value)
				}
			}
			offset += count
		}
	} else {
		for index := range header.InterPartitions {
			partition := &header.InterPartitions[index]
			if slice.ReferenceCount[0] > 1 {
				refContext, _ := partitionContext(index)
				value, decodeErr := DecodeCABACRefIndex(models, decoder, refContext, int(slice.ReferenceCount[0]-1))
				if decodeErr != nil {
					return MacroblockHeader{}, decodeErr
				}
				partition.ReferenceIndex = uint64(value)
			}
		}
	}
	for index := range header.InterPartitions {
		partition := &header.InterPartitions[index]
		_, mvdContext := partitionContext(index)
		for component := range 2 {
			value, decodeErr := DecodeCABACMVD(models, decoder, component == 1, mvdContext[component][0], mvdContext[component][1])
			if decodeErr != nil {
				return MacroblockHeader{}, decodeErr
			}
			partition.MotionDifference[component] = int64(value)
		}
	}
	header.ReferenceIndexL0 = header.InterPartitions[0].ReferenceIndex
	header.MotionVectorDifference = header.InterPartitions[0].MotionDifference
	luma, chroma, err := DecodeCABACCodedBlockPattern(models, decoder, left.cbp(), top.cbp(), slice.SPS.ChromaFormat != 0)
	if err != nil {
		return MacroblockHeader{}, err
	}
	header.CodedBlockPatternLuma, header.CodedBlockPatternChroma = luma, chroma
	if luma != 0 || chroma != 0 {
		delta, decodeErr := DecodeCABACMBQPDelta(models, decoder, previousQPContext)
		if decodeErr != nil {
			return MacroblockHeader{}, decodeErr
		}
		header.QPDelta = int64(delta)
	}
	return header, nil
}

// decodeCABACBMacroblockHeaderWithContexts decodes progressive inter B
// macroblocks. The callback supplies the list-specific
// ref_idx and mvd context conditions at each partition origin.
func decodeCABACBMacroblockHeaderWithContexts(models *CABACModels, decoder cabacSyntaxDecoder, slice SliceHeader,
	left, top CABACMacroblockNeighbour, neighbourNotSkipOrDirect int, previousQPContext bool,
	contexts func(index, list int, partition InterPartition) (int, [2][2]int),
) (MacroblockHeader, error) {
	rawType, err := DecodeCABACBMacroblockType(models, decoder, neighbourNotSkipOrDirect)
	if err != nil {
		return MacroblockHeader{}, err
	}
	if rawType > 22 {
		header, decodeErr := decodeCABACIntraHeaderAfterType(models, decoder, slice, left, top, previousQPContext, rawType-23)
		if decodeErr != nil {
			return MacroblockHeader{}, decodeErr
		}
		header.RawType = rawType
		return header, nil
	}
	header := MacroblockHeader{RawType: rawType, Kind: MacroblockInter}
	if rawType == 0 {
		header.Direct = true
		header.InterPartitions = []InterPartition{{Width: 16, Height: 16, Direct: true}}
	} else if rawType == 22 {
		for sub := range 4 {
			typeValue, decodeErr := DecodeCABACSubMacroblockType(models, decoder, SliceB)
			if decodeErr != nil {
				return MacroblockHeader{}, fmt.Errorf("CABAC B sub_mb_type %d: %w", sub, decodeErr)
			}
			header.SubMacroblockTypes[sub] = typeValue
			header.InterPartitions = append(header.InterPartitions, bSubPartitions(sub, typeValue)...)
		}
	} else {
		header.InterPartitions = bMacroblockPartitions(rawType)
	}

	partitionContext := func(index, list int) (int, [2][2]int) {
		partition := header.InterPartitions[index]
		ref, mvd := contexts(index, list, partition)
		for side, coordinate := range [2][2]int{{partition.X - 1, partition.Y}, {partition.X, partition.Y - 1}} {
			if coordinate[0] < 0 || coordinate[1] < 0 {
				continue
			}
			for previous := index - 1; previous >= 0; previous-- {
				candidate := header.InterPartitions[previous]
				if coordinate[0] < candidate.X || coordinate[0] >= candidate.X+candidate.Width ||
					coordinate[1] < candidate.Y || coordinate[1] >= candidate.Y+candidate.Height {
					continue
				}
				usesList := candidate.UseList0
				reference := candidate.ReferenceIndex
				difference := candidate.MotionDifference
				if list == 1 {
					usesList = candidate.UseList1
					reference = candidate.ReferenceIndexL1
					difference = candidate.MotionDifferenceL1
				}
				if usesList && !candidate.Direct && reference > 0 {
					ref += side + 1
				}
				if usesList && !candidate.Direct {
					mvd[0][side], mvd[1][side] = absInt(int(difference[0])), absInt(int(difference[1]))
				} else {
					mvd[0][side], mvd[1][side] = 0, 0
				}
				break
			}
		}
		return ref, mvd
	}
	for list := range 2 {
		for index := 0; index < len(header.InterPartitions); {
			partition := &header.InterPartitions[index]
			usesList := partition.UseList0
			if list == 1 {
				usesList = partition.UseList1
			}
			count := 1
			if rawType == 22 {
				sub := (partition.Y/8)*2 + partition.X/8
				count = len(bSubPartitions(sub, header.SubMacroblockTypes[sub]))
			}
			if partition.Direct || !usesList || slice.ReferenceCount[list] <= 1 {
				index += count
				continue
			}
			refContext, _ := partitionContext(index, list)
			value, decodeErr := DecodeCABACRefIndex(models, decoder, refContext, int(slice.ReferenceCount[list]-1))
			if decodeErr != nil {
				return MacroblockHeader{}, decodeErr
			}
			if list == 0 {
				for part := index; part < index+count; part++ {
					header.InterPartitions[part].ReferenceIndex = uint64(value)
				}
			} else {
				for part := index; part < index+count; part++ {
					header.InterPartitions[part].ReferenceIndexL1 = uint64(value)
				}
			}
			index += count
		}
	}
	for list := range 2 {
		for index := range header.InterPartitions {
			partition := &header.InterPartitions[index]
			usesList := partition.UseList0
			if list == 1 {
				usesList = partition.UseList1
			}
			if !usesList || partition.Direct {
				continue
			}
			_, mvdContext := partitionContext(index, list)
			for component := range 2 {
				neighbours := mvdContext[component]
				value, decodeErr := DecodeCABACMVD(models, decoder, component == 1, neighbours[0], neighbours[1])
				if decodeErr != nil {
					return MacroblockHeader{}, decodeErr
				}
				if list == 0 {
					partition.MotionDifference[component] = int64(value)
				} else {
					partition.MotionDifferenceL1[component] = int64(value)
				}
			}
		}
	}
	luma, chroma, err := DecodeCABACCodedBlockPattern(models, decoder, left.cbp(), top.cbp(), slice.SPS.ChromaFormat != 0)
	if err != nil {
		return MacroblockHeader{}, err
	}
	header.CodedBlockPatternLuma, header.CodedBlockPatternChroma = luma, chroma
	if luma != 0 || chroma != 0 {
		delta, decodeErr := DecodeCABACMBQPDelta(models, decoder, previousQPContext)
		if decodeErr != nil {
			return MacroblockHeader{}, decodeErr
		}
		header.QPDelta = int64(delta)
	}
	return header, nil
}
