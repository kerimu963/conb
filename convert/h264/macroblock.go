package h264

import (
	"errors"
	"fmt"
)

var (
	ErrInterMacroblockUnsupported = errors.New("inter-predicted macroblock is not implemented")
)

type MacroblockKind uint8

const (
	MacroblockIntra4x4 MacroblockKind = iota
	MacroblockIntra16x16
	MacroblockPCM
	MacroblockInter
)

// IntraPredictionMode describes one Intra4x4 mode syntax element. When
// Previous is true, Rem is inferred from neighbouring blocks and is not read.
type IntraPredictionMode struct {
	Previous bool
	Rem      uint8
}

// InterPartition describes one list-0 prediction region in luma pixels.
type InterPartition struct {
	X, Y, Width, Height int
	UseList0, UseList1  bool
	Direct              bool
	ReferenceIndex      uint64
	MotionDifference    [2]int64
	ReferenceIndexL1    uint64
	MotionDifferenceL1  [2]int64
}

// MacroblockHeader contains syntax needed before residual decoding.
type MacroblockHeader struct {
	RawType                 uint64
	Kind                    MacroblockKind
	Intra16x16Prediction    uint8
	Intra4x4Prediction      [16]IntraPredictionMode
	IntraChromaPrediction   uint64
	CodedBlockPatternLuma   uint8
	CodedBlockPatternChroma uint8
	QPDelta                 int64
	ReferenceIndexL0        uint64
	MotionVectorDifference  [2]int64
	InterPartitions         []InterPartition
	SubMacroblockTypes      [4]uint64
	Direct                  bool
}

// ParseMacroblockHeader parses an intra macroblock. PCM sample data follows
// the returned header and is consumed by the picture decoder.
func ParseMacroblockHeader(r *BitReader, slice SliceHeader) (MacroblockHeader, error) {
	if r == nil {
		return MacroblockHeader{}, malformed("nil macroblock bit reader")
	}
	rawType, err := r.ReadUE()
	if err != nil {
		return MacroblockHeader{}, malformed("mb_type is truncated")
	}
	header := MacroblockHeader{RawType: rawType}
	intraType, inter, valid := mapIntraMacroblockType(slice.Type, rawType)
	if !valid {
		return MacroblockHeader{}, malformed(fmt.Sprintf("invalid mb_type %d for %s slice", rawType, slice.Type))
	}
	if inter {
		header.Kind = MacroblockInter
		if slice.Type == SliceB {
			if rawType > 22 {
				return header, ErrInterMacroblockUnsupported
			}
			if rawType == 0 {
				header.Direct = true
				header.InterPartitions = []InterPartition{{Width: 16, Height: 16, Direct: true}}
			} else if rawType == 22 {
				for sub := range 4 {
					typeValue, readErr := r.ReadUE()
					if readErr != nil || typeValue > 12 {
						return MacroblockHeader{}, malformed("invalid B sub_mb_type")
					}
					header.SubMacroblockTypes[sub] = typeValue
					header.InterPartitions = append(header.InterPartitions, bSubPartitions(sub, typeValue)...)
				}
			} else {
				header.InterPartitions = bMacroblockPartitions(rawType)
			}
			if rawType == 22 {
				offset := 0
				for sub := range 4 {
					parts := bSubPartitions(sub, header.SubMacroblockTypes[sub])
					if parts[0].UseList0 && slice.ReferenceCount[0] > 1 {
						value, readErr := r.ReadUE()
						if readErr != nil || value >= slice.ReferenceCount[0] {
							return MacroblockHeader{}, malformed("invalid B ref_idx_l0")
						}
						for index := offset; index < offset+len(parts); index++ {
							header.InterPartitions[index].ReferenceIndex = value
						}
					}
					offset += len(parts)
				}
			} else {
				for index := range header.InterPartitions {
					partition := &header.InterPartitions[index]
					if partition.UseList0 && slice.ReferenceCount[0] > 1 {
						if partition.ReferenceIndex, err = r.ReadUE(); err != nil || partition.ReferenceIndex >= slice.ReferenceCount[0] {
							return MacroblockHeader{}, malformed("invalid B ref_idx_l0")
						}
					}
				}
			}
			if rawType == 22 {
				offset := 0
				for sub := range 4 {
					parts := bSubPartitions(sub, header.SubMacroblockTypes[sub])
					if parts[0].UseList1 && slice.ReferenceCount[1] > 1 {
						value, readErr := r.ReadUE()
						if readErr != nil || value >= slice.ReferenceCount[1] {
							return MacroblockHeader{}, malformed("invalid B ref_idx_l1")
						}
						for index := offset; index < offset+len(parts); index++ {
							header.InterPartitions[index].ReferenceIndexL1 = value
						}
					}
					offset += len(parts)
				}
			} else {
				for index := range header.InterPartitions {
					partition := &header.InterPartitions[index]
					if partition.UseList1 && slice.ReferenceCount[1] > 1 {
						if partition.ReferenceIndexL1, err = r.ReadUE(); err != nil || partition.ReferenceIndexL1 >= slice.ReferenceCount[1] {
							return MacroblockHeader{}, malformed("invalid B ref_idx_l1")
						}
					}
				}
			}
			for index := range header.InterPartitions {
				partition := &header.InterPartitions[index]
				if !partition.UseList0 {
					continue
				}
				for component := range 2 {
					if partition.MotionDifference[component], err = r.ReadSE(); err != nil {
						return MacroblockHeader{}, malformed("B mvd_l0 is truncated")
					}
				}
			}
			for index := range header.InterPartitions {
				partition := &header.InterPartitions[index]
				if !partition.UseList1 {
					continue
				}
				for component := range 2 {
					if partition.MotionDifferenceL1[component], err = r.ReadSE(); err != nil {
						return MacroblockHeader{}, malformed("B mvd_l1 is truncated")
					}
				}
			}
			if err = parseInterCodedBlockPattern(r, &header); err != nil {
				return MacroblockHeader{}, err
			}
			return header, nil
		}
		if slice.Type != SliceP || rawType > 4 {
			return header, ErrInterMacroblockUnsupported
		}
		switch rawType {
		case 0:
			header.InterPartitions = []InterPartition{{Width: 16, Height: 16, UseList0: true}}
		case 1:
			header.InterPartitions = []InterPartition{{Width: 16, Height: 8, UseList0: true}, {Y: 8, Width: 16, Height: 8, UseList0: true}}
		case 2:
			header.InterPartitions = []InterPartition{{Width: 8, Height: 16, UseList0: true}, {X: 8, Width: 8, Height: 16, UseList0: true}}
		case 3, 4:
			for sub := range 4 {
				typeValue, readErr := r.ReadUE()
				if readErr != nil || typeValue > 3 {
					return MacroblockHeader{}, malformed("invalid P sub_mb_type")
				}
				header.SubMacroblockTypes[sub] = typeValue
				header.InterPartitions = append(header.InterPartitions, pSubPartitions(sub, typeValue)...)
			}
		}
		if slice.ReferenceCount[0] > 1 {
			if rawType == 3 {
				partitionOffset := 0
				for sub := range 4 {
					value, readErr := r.ReadUE()
					if readErr != nil || value >= slice.ReferenceCount[0] {
						return MacroblockHeader{}, malformed("invalid ref_idx_l0")
					}
					count := len(pSubPartitions(sub, header.SubMacroblockTypes[sub]))
					for index := partitionOffset; index < partitionOffset+count; index++ {
						header.InterPartitions[index].ReferenceIndex = value
					}
					partitionOffset += count
				}
			} else if rawType != 4 {
				for index := range header.InterPartitions {
					value, readErr := r.ReadUE()
					if readErr != nil || value >= slice.ReferenceCount[0] {
						return MacroblockHeader{}, malformed("invalid ref_idx_l0")
					}
					header.InterPartitions[index].ReferenceIndex = value
				}
			}
		}
		for index := range header.InterPartitions {
			for component := range 2 {
				if header.InterPartitions[index].MotionDifference[component], err = r.ReadSE(); err != nil {
					return MacroblockHeader{}, malformed("mvd_l0 is truncated")
				}
			}
		}
		header.ReferenceIndexL0 = header.InterPartitions[0].ReferenceIndex
		header.MotionVectorDifference = header.InterPartitions[0].MotionDifference
		if err = parseInterCodedBlockPattern(r, &header); err != nil {
			return MacroblockHeader{}, err
		}
		return header, nil
	}
	if intraType == 25 {
		header.Kind = MacroblockPCM
		return header, nil
	}
	if intraType == 0 {
		header.Kind = MacroblockIntra4x4
		for block := range header.Intra4x4Prediction {
			previous, readErr := r.ReadBit()
			if readErr != nil {
				return MacroblockHeader{}, malformed("prev_intra4x4_pred_mode_flag is truncated")
			}
			header.Intra4x4Prediction[block].Previous = previous != 0
			if previous == 0 {
				remaining, readErr := r.ReadBits(3)
				if readErr != nil {
					return MacroblockHeader{}, malformed("rem_intra4x4_pred_mode is truncated")
				}
				header.Intra4x4Prediction[block].Rem = uint8(remaining)
			}
		}
	} else {
		header.Kind = MacroblockIntra16x16
		index := intraType - 1
		header.Intra16x16Prediction = uint8(index % 4)
		header.CodedBlockPatternChroma = uint8(index/4) % 3
		if index >= 12 {
			header.CodedBlockPatternLuma = 15
		}
	}
	if slice.SPS.ChromaFormat != 0 && !slice.SPS.SeparateColourPlane {
		if header.IntraChromaPrediction, err = r.ReadUE(); err != nil || header.IntraChromaPrediction > 3 {
			return MacroblockHeader{}, malformed("invalid intra_chroma_pred_mode")
		}
	}
	if header.Kind == MacroblockIntra4x4 {
		codeNumber, readErr := r.ReadUE()
		if readErr != nil || codeNumber >= 48 {
			return MacroblockHeader{}, malformed("invalid coded_block_pattern")
		}
		pattern := intraCBPByCodeNumber[codeNumber]
		header.CodedBlockPatternLuma = pattern & 15
		header.CodedBlockPatternChroma = pattern >> 4
	}
	if header.CodedBlockPatternLuma != 0 || header.CodedBlockPatternChroma != 0 || header.Kind == MacroblockIntra16x16 {
		if header.QPDelta, err = r.ReadSE(); err != nil {
			return MacroblockHeader{}, malformed("mb_qp_delta is truncated")
		}
		if slice.SPS.BitDepthLuma < 8 || slice.SPS.BitDepthLuma > 14 {
			return MacroblockHeader{}, malformed("invalid luma bit depth for mb_qp_delta")
		}
		qpDepthOffset := int64(3 * (slice.SPS.BitDepthLuma - 8))
		if header.QPDelta < -26-qpDepthOffset || header.QPDelta > 25+qpDepthOffset {
			return MacroblockHeader{}, malformed("mb_qp_delta is out of range")
		}
	}
	return header, nil
}

func bSubPartitions(sub int, subType uint64) []InterPartition {
	x, y := (sub%2)*8, (sub/2)*8
	mode := func(value uint64) (list0, list1 bool) {
		switch value {
		case 1, 4, 5, 10:
			return true, false
		case 2, 6, 7, 11:
			return false, true
		default:
			return true, true
		}
	}
	list0, list1 := mode(subType)
	if subType == 0 {
		return []InterPartition{{X: x, Y: y, Width: 8, Height: 8, Direct: true}}
	}
	makePartition := func(px, py, width, height int) InterPartition {
		return InterPartition{X: px, Y: py, Width: width, Height: height, UseList0: list0, UseList1: list1}
	}
	switch subType {
	case 1, 2, 3:
		return []InterPartition{makePartition(x, y, 8, 8)}
	case 4, 6, 8:
		return []InterPartition{makePartition(x, y, 8, 4), makePartition(x, y+4, 8, 4)}
	case 5, 7, 9:
		return []InterPartition{makePartition(x, y, 4, 8), makePartition(x+4, y, 4, 8)}
	default:
		return []InterPartition{
			makePartition(x, y, 4, 4), makePartition(x+4, y, 4, 4),
			makePartition(x, y+4, 4, 4), makePartition(x+4, y+4, 4, 4),
		}
	}
}

func bMacroblockPartitions(raw uint64) []InterPartition {
	mode := func(value uint8) (list0, list1 bool) {
		return value == 0 || value == 2, value == 1 || value == 2
	}
	if raw <= 3 {
		list0, list1 := mode(uint8(raw - 1))
		return []InterPartition{{Width: 16, Height: 16, UseList0: list0, UseList1: list1}}
	}
	modes := [][2]uint8{
		{0, 0}, {0, 0}, {1, 1}, {1, 1}, {0, 1}, {0, 1}, {1, 0}, {1, 0},
		{0, 2}, {0, 2}, {1, 2}, {1, 2}, {2, 0}, {2, 0}, {2, 1}, {2, 1}, {2, 2}, {2, 2},
	}
	pair := modes[raw-4]
	verticalSplit := raw%2 == 1 // 8x16; even values are 16x8.
	result := make([]InterPartition, 2)
	for index := range 2 {
		result[index].UseList0, result[index].UseList1 = mode(pair[index])
		if verticalSplit {
			result[index].X, result[index].Width, result[index].Height = index*8, 8, 16
		} else {
			result[index].Y, result[index].Width, result[index].Height = index*8, 16, 8
		}
	}
	return result
}

func parseInterCodedBlockPattern(r *BitReader, header *MacroblockHeader) error {
	codeNumber, err := r.ReadUE()
	if err != nil || codeNumber >= 48 {
		return malformed("invalid inter coded_block_pattern")
	}
	pattern := interCBPByCodeNumber[codeNumber]
	header.CodedBlockPatternLuma = pattern & 15
	header.CodedBlockPatternChroma = pattern >> 4
	if pattern != 0 {
		if header.QPDelta, err = r.ReadSE(); err != nil {
			return malformed("mb_qp_delta is truncated")
		}
	}
	return nil
}

func pSubPartitions(sub int, subType uint64) []InterPartition {
	x, y := (sub%2)*8, (sub/2)*8
	switch subType {
	case 0:
		return []InterPartition{{X: x, Y: y, Width: 8, Height: 8, UseList0: true}}
	case 1:
		return []InterPartition{{X: x, Y: y, Width: 8, Height: 4, UseList0: true}, {X: x, Y: y + 4, Width: 8, Height: 4, UseList0: true}}
	case 2:
		return []InterPartition{{X: x, Y: y, Width: 4, Height: 8, UseList0: true}, {X: x + 4, Y: y, Width: 4, Height: 8, UseList0: true}}
	default:
		return []InterPartition{
			{X: x, Y: y, Width: 4, Height: 4, UseList0: true}, {X: x + 4, Y: y, Width: 4, Height: 4, UseList0: true},
			{X: x, Y: y + 4, Width: 4, Height: 4, UseList0: true}, {X: x + 4, Y: y + 4, Width: 4, Height: 4, UseList0: true},
		}
	}
}

// Values indexed by coded-block-pattern, containing the Intra codeNum from
// H.264 Table 9-4. The inverse map is built once from this specification data.
var intraCBPCodeNumber = [48]uint8{
	3, 29, 30, 17, 31, 18, 37, 8, 32, 38, 19, 9, 20, 10, 11, 2,
	16, 33, 34, 21, 35, 22, 39, 4, 36, 40, 23, 5, 24, 6, 7, 1,
	41, 42, 43, 25, 44, 26, 46, 12, 45, 47, 27, 13, 28, 14, 15, 0,
}

var intraCBPByCodeNumber = func() [48]uint8 {
	var inverse [48]uint8
	for pattern, codeNumber := range intraCBPCodeNumber {
		inverse[codeNumber] = uint8(pattern)
	}
	return inverse
}()

// H.264 Table 9-4 mapping for inter-predicted macroblocks.
var interCBPByCodeNumber = [48]uint8{
	0, 16, 1, 2, 4, 8, 32, 3, 5, 10, 12, 15, 47, 7, 11, 13,
	14, 6, 9, 31, 35, 37, 42, 44, 33, 34, 36, 40, 39, 43, 45, 46,
	17, 18, 20, 24, 19, 21, 26, 28, 23, 27, 29, 30, 22, 25, 38, 41,
}

func mapIntraMacroblockType(sliceType SliceType, raw uint64) (intraType uint64, inter, valid bool) {
	switch sliceType {
	case SliceI:
		return raw, false, raw <= 25
	case SliceSI:
		if raw == 0 { // SI macroblock has its own prediction syntax.
			return 0, true, true
		}
		return raw - 1, false, raw <= 26
	case SliceP, SliceSP:
		if raw < 5 {
			return 0, true, true
		}
		return raw - 5, false, raw <= 30
	case SliceB:
		if raw < 23 {
			return 0, true, true
		}
		return raw - 23, false, raw <= 48
	default:
		return 0, false, false
	}
}
