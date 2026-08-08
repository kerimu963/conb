package h264

import "fmt"

type cabacTerminateDecoder interface {
	cabacBinDecoder
	DecodeTerminate() (uint8, error)
}

type cabacBypassDecoder interface {
	cabacBinDecoder
	DecodeBypass() (uint8, error)
}

// CABACIntra4x4Mode is the pair of syntax elements used to derive one luma
// Intra4x4 prediction mode. Rem is meaningful only when Prev is false.
type CABACIntra4x4Mode struct {
	Prev bool
	Rem  uint8
}

// CABACCBPNeighbour is the decoded coded_block_pattern state of an adjacent
// macroblock. PCM neighbours behave as though every block were coded.
type CABACCBPNeighbour struct {
	Available bool
	PCM       bool
	Luma      uint8
	Chroma    uint8
}

// DecodeCABACMBQPDelta decodes the unary binarization of mb_qp_delta. The
// previous flag is false at a slice boundary and after skip/PCM macroblocks.
func DecodeCABACMBQPDelta(models *CABACModels, decoder cabacBinDecoder, previousNonZero bool) (int, error) {
	firstContext := 60
	if previousNonZero {
		firstContext++
	}
	bin, err := models.Decode(decoder, firstContext)
	if err != nil || bin == 0 {
		return 0, err
	}
	ones := 1
	for {
		context := 62
		if ones > 1 {
			context = 63
		}
		bin, err = models.Decode(decoder, context)
		if err != nil {
			return 0, err
		}
		if bin == 0 {
			break
		}
		ones++
		if ones > 102 {
			return 0, malformed("CABAC mb_qp_delta unary value is too large")
		}
	}
	if ones&1 != 0 {
		return (ones + 1) / 2, nil
	}
	return -ones / 2, nil
}

// DecodeCABACIntraChromaMode decodes intra_chroma_pred_mode (range 0..3).
// leftNonZero and topNonZero are the availability-conditioned neighbour modes.
func DecodeCABACIntraChromaMode(models *CABACModels, decoder cabacBinDecoder, leftNonZero, topNonZero bool) (uint8, error) {
	context := 64
	if leftNonZero {
		context++
	}
	if topNonZero {
		context++
	}
	first, err := models.Decode(decoder, context)
	if err != nil || first == 0 {
		return 0, err
	}
	value := uint8(1)
	for value < 3 {
		bin, decodeErr := models.Decode(decoder, 67)
		if decodeErr != nil {
			return 0, decodeErr
		}
		if bin == 0 {
			break
		}
		value++
	}
	return value, nil
}

// DecodeCABACIntra4x4Mode decodes prev_intra4x4_pred_mode_flag and, when
// required, the three-bin rem_intra4x4_pred_mode value.
func DecodeCABACIntra4x4Mode(models *CABACModels, decoder cabacBinDecoder) (CABACIntra4x4Mode, error) {
	previous, err := models.Decode(decoder, 68)
	if err != nil {
		return CABACIntra4x4Mode{}, err
	}
	if previous != 0 {
		return CABACIntra4x4Mode{Prev: true}, nil
	}
	var remaining uint8
	for binIndex := range 3 {
		bin, decodeErr := models.Decode(decoder, 69)
		if decodeErr != nil {
			return CABACIntra4x4Mode{}, decodeErr
		}
		remaining |= bin << binIndex
	}
	return CABACIntra4x4Mode{Rem: remaining}, nil
}

// DecodeCABACRefIndex decodes ref_idx_lX. neighbourCount is the sum of the
// normative left/top condition terms and maximum is num_ref_idx_active_minus1.
func DecodeCABACRefIndex(models *CABACModels, decoder cabacBinDecoder, neighbourCount, maximum int) (int, error) {
	if neighbourCount < 0 || neighbourCount > 2 || maximum < 0 || maximum > 31 {
		return 0, fmt.Errorf("invalid CABAC ref_idx parameters")
	}
	if maximum == 0 {
		return 0, nil
	}
	bin, err := models.Decode(decoder, 54+neighbourCount)
	if err != nil || bin == 0 {
		return 0, err
	}
	value := 1
	for {
		context := 58
		if value > 1 {
			context = 59
		}
		bin, err = models.Decode(decoder, context)
		if err != nil {
			return 0, err
		}
		if bin == 0 {
			break
		}
		value++
		if value > 31 {
			return 0, malformed("CABAC ref_idx unary value is too large")
		}
	}
	return value, nil
}

// DecodeCABACCodedBlockPattern decodes the four luma CBP bits followed by the
// truncated-unary chroma CBP value for 4:2:0/4:2:2 pictures.
func DecodeCABACCodedBlockPattern(models *CABACModels, decoder cabacBinDecoder, left, top CABACCBPNeighbour, hasChroma bool) (uint8, uint8, error) {
	var luma uint8
	for block := 0; block < 4; block++ {
		x, y := block&1, block>>1
		leftCoded := true
		if x != 0 {
			leftCoded = luma&(1<<uint(block-1)) != 0
		} else if left.Available && !left.PCM {
			neighbourBlock := 1 + y*2
			leftCoded = left.Luma&(1<<uint(neighbourBlock)) != 0
		}
		topCoded := true
		if y != 0 {
			topCoded = luma&(1<<uint(block-2)) != 0
		} else if top.Available && !top.PCM {
			neighbourBlock := 2 + x
			topCoded = top.Luma&(1<<uint(neighbourBlock)) != 0
		}
		increment := 0
		if !leftCoded {
			increment++
		}
		if !topCoded {
			increment += 2
		}
		bin, err := models.Decode(decoder, 73+increment)
		if err != nil {
			return 0, 0, err
		}
		if bin != 0 {
			luma |= 1 << uint(block)
		}
	}
	if !hasChroma {
		return luma, 0, nil
	}
	firstIncrement := cabacChromaCBPIncrement(left, 0) + 2*cabacChromaCBPIncrement(top, 0)
	first, err := models.Decode(decoder, 77+firstIncrement)
	if err != nil || first == 0 {
		return luma, 0, err
	}
	secondIncrement := cabacChromaCBPIncrement(left, 1) + 2*cabacChromaCBPIncrement(top, 1)
	second, err := models.Decode(decoder, 81+secondIncrement)
	if err != nil {
		return 0, 0, err
	}
	return luma, 1 + second, nil
}

func cabacChromaCBPIncrement(neighbour CABACCBPNeighbour, binIndex int) int {
	if !neighbour.Available {
		return 0
	}
	if neighbour.PCM {
		return 1
	}
	if binIndex == 0 {
		if neighbour.Chroma != 0 {
			return 1
		}
		return 0
	}
	if neighbour.Chroma == 2 {
		return 1
	}
	return 0
}

// DecodeCABACIMacroblockType decodes Table 9-36's I-slice mb_type
// binarization. neighbourNonI4x4 is the sum of available left/top neighbours
// whose type is not I_NxN.
func DecodeCABACIMacroblockType(models *CABACModels, decoder cabacTerminateDecoder, neighbourNonI4x4 int) (uint64, error) {
	if neighbourNonI4x4 < 0 || neighbourNonI4x4 > 2 {
		return 0, fmt.Errorf("invalid CABAC I mb_type neighbour count")
	}
	return decodeCABACIntraTypeSuffix(models, decoder, 3, 3+neighbourNonI4x4)
}

// DecodeCABACPMacroblockType decodes P_L0_16x16, P_16x8, P_8x16, P_8x8,
// and the I macroblock suffix used in P slices. The prohibited P_8x8ref0 value
// has no CABAC bin string and is therefore never returned.
func DecodeCABACPMacroblockType(models *CABACModels, decoder cabacTerminateDecoder) (uint64, error) {
	first, err := models.Decode(decoder, 14)
	if err != nil {
		return 0, err
	}
	if first != 0 {
		intra, decodeErr := decodeCABACIntraTypeSuffix(models, decoder, 17, 17)
		if decodeErr != nil {
			return 0, decodeErr
		}
		return intra + 5, nil
	}
	second, err := models.Decode(decoder, 15)
	if err != nil {
		return 0, err
	}
	third, err := models.Decode(decoder, 16+int(second))
	if err != nil {
		return 0, err
	}
	if second == 0 {
		if third == 0 {
			return 0, nil
		}
		return 3, nil
	}
	if third == 0 {
		return 2, nil
	}
	return 1, nil
}

func decodeCABACIntraTypeSuffix(models *CABACModels, decoder cabacTerminateDecoder, offset, firstContext int) (uint64, error) {
	first, err := models.Decode(decoder, firstContext)
	if err != nil || first == 0 {
		return 0, err // I_NxN (I_4x4 for supported profiles)
	}
	pcm, err := decoder.DecodeTerminate()
	if err != nil {
		return 0, err
	}
	if pcm != 0 {
		return 25, nil
	}
	lumaContext := offset + 3
	if offset != 3 {
		lumaContext = offset + 1
	}
	lumaFlag, err := models.Decode(decoder, lumaContext)
	if err != nil {
		return 0, err
	}
	chromaPresent, err := models.Decode(decoder, lumaContext+1)
	if err != nil {
		return 0, err
	}
	chroma := uint8(0)
	if chromaPresent != 0 {
		second, decodeErr := models.Decode(decoder, lumaContext+2)
		if decodeErr != nil {
			return 0, decodeErr
		}
		chroma = 1 + second
	}
	predictionHigh, err := models.Decode(decoder, lumaContext+3)
	if err != nil {
		return 0, err
	}
	predictionLow, err := models.Decode(decoder, lumaContext+4)
	if err != nil {
		return 0, err
	}
	prediction := predictionHigh<<1 | predictionLow
	return 1 + uint64(prediction) + 4*uint64(chroma) + 12*uint64(lumaFlag), nil
}

var cabacBMacroblockTypeBins = map[string]uint64{
	"0": 0, "100": 1, "101": 2,
	"110000": 3, "110001": 4, "110010": 5, "110011": 6,
	"110100": 7, "110101": 8, "110110": 9, "110111": 10,
	"111110":  11,
	"1110000": 12, "1110001": 13, "1110010": 14, "1110011": 15,
	"1110100": 16, "1110101": 17, "1110110": 18, "1110111": 19,
	"1111000": 20, "1111001": 21, "111111": 22,
}

// DecodeCABACBMacroblockType decodes all B-slice inter types and the I-type
// suffix. neighbourNotSkipOrDirect is the normative left/top condition sum.
func DecodeCABACBMacroblockType(models *CABACModels, decoder cabacTerminateDecoder, neighbourNotSkipOrDirect int) (uint64, error) {
	if neighbourNotSkipOrDirect < 0 || neighbourNotSkipOrDirect > 2 {
		return 0, fmt.Errorf("invalid CABAC B mb_type neighbour count")
	}
	prefix := ""
	for binIndex := 0; binIndex < 7; binIndex++ {
		context := 32
		switch binIndex {
		case 0:
			context = 27 + neighbourNotSkipOrDirect
		case 1:
			context = 30
		case 2:
			context = 32
			if len(prefix) > 1 && prefix[1] == '1' {
				context = 31
			}
		}
		bin, err := models.Decode(decoder, context)
		if err != nil {
			return 0, err
		}
		prefix += string('0' + bin)
		if value, found := cabacBMacroblockTypeBins[prefix]; found {
			return value, nil
		}
		if prefix == "111101" {
			intra, decodeErr := decodeCABACIntraTypeSuffix(models, decoder, 32, 32)
			if decodeErr != nil {
				return 0, decodeErr
			}
			return 23 + intra, nil
		}
	}
	return 0, malformed("invalid CABAC B mb_type bin string")
}

var cabacPSubMacroblockTypeBins = map[string]uint64{"1": 0, "00": 1, "011": 2, "010": 3}
var cabacBSubMacroblockTypeBins = map[string]uint64{
	"0": 0, "100": 1, "101": 2,
	"11000": 3, "11001": 4, "11010": 5, "11011": 6,
	"111000": 7, "111001": 8, "111010": 9, "111011": 10,
	"11110": 11, "11111": 12,
}

// DecodeCABACSubMacroblockType decodes Table 9-38 for P or B slices.
func DecodeCABACSubMacroblockType(models *CABACModels, decoder cabacBinDecoder, sliceType SliceType) (uint64, error) {
	table, maximum := cabacPSubMacroblockTypeBins, 3
	if sliceType == SliceB {
		table, maximum = cabacBSubMacroblockTypeBins, 6
	} else if sliceType != SliceP && sliceType != SliceSP {
		return 0, fmt.Errorf("CABAC sub_mb_type is invalid for %s slice", sliceType)
	}
	prefix := ""
	for binIndex := 0; binIndex < maximum; binIndex++ {
		context := 21 + binIndex
		if sliceType == SliceB {
			context = 36 + binIndex
			if binIndex == 2 && len(prefix) > 1 && prefix[1] == '0' {
				context = 39
			}
			if binIndex >= 3 {
				context = 39
			}
		}
		bin, err := models.Decode(decoder, context)
		if err != nil {
			return 0, err
		}
		prefix += string('0' + bin)
		if value, found := table[prefix]; found {
			return value, nil
		}
	}
	return 0, malformed("invalid CABAC sub_mb_type bin string")
}

// DecodeCABACMVD decodes one signed mvd_lX component using UEG3 with uCoff=9.
// The neighbour magnitudes are the already-normalized absMvdCompA/B values.
func DecodeCABACMVD(models *CABACModels, decoder cabacBypassDecoder, vertical bool, neighbourA, neighbourB int) (int, error) {
	if neighbourA < 0 || neighbourB < 0 {
		return 0, fmt.Errorf("negative CABAC neighbouring MVD magnitude")
	}
	offset := 40
	if vertical {
		offset = 47
	}
	sum := neighbourA + neighbourB
	firstIncrement := 0
	if sum > 32 {
		firstIncrement = 2
	} else if sum >= 3 {
		firstIncrement = 1
	}
	absValue := 0
	for binIndex := 0; binIndex < 9; binIndex++ {
		increment := 6
		switch binIndex {
		case 0:
			increment = firstIncrement
		case 1:
			increment = 3
		case 2:
			increment = 4
		case 3:
			increment = 5
		}
		bin, err := models.Decode(decoder, offset+increment)
		if err != nil {
			return 0, err
		}
		if bin == 0 {
			break
		}
		absValue++
	}
	if absValue == 9 {
		suffix, err := decodeCABACUEGSuffix(decoder, 3)
		if err != nil {
			return 0, err
		}
		absValue += suffix
	}
	if absValue == 0 {
		return 0, nil
	}
	sign, err := decoder.DecodeBypass()
	if err != nil {
		return 0, err
	}
	if sign != 0 {
		return -absValue, nil
	}
	return absValue, nil
}

func decodeCABACUEGSuffix(decoder cabacBypassDecoder, order uint) (int, error) {
	prefixValue := uint64(0)
	for {
		bin, err := decoder.DecodeBypass()
		if err != nil {
			return 0, err
		}
		if bin == 0 {
			break
		}
		if order >= 31 {
			return 0, malformed("CABAC UEG suffix is too large")
		}
		prefixValue += uint64(1) << order
		order++
	}
	info := uint64(0)
	for remaining := order; remaining > 0; remaining-- {
		bin, err := decoder.DecodeBypass()
		if err != nil {
			return 0, err
		}
		info |= uint64(bin) << (remaining - 1)
	}
	value := prefixValue + info
	if value > uint64(^uint(0)>>1) {
		return 0, malformed("CABAC UEG suffix overflows int")
	}
	return int(value), nil
}
