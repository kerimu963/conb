package h264

import "fmt"

// CABACBlockCategory identifies the five 4x4-era residual block categories in
// H.264 Table 9-40. 8x8-transform and field categories use separate contexts.
type CABACBlockCategory uint8

const (
	CABACLumaDC CABACBlockCategory = iota
	CABACLumaAC
	CABACLuma4x4
	CABACChromaDC
	CABACChromaAC
)

var cabacBlockMaximum = [...]int{16, 15, 16, 4, 15}
var cabacCodedBlockOffset = [...]int{0, 4, 8, 12, 16}
var cabacSignificantOffset = [...]int{0, 15, 29, 44, 47}
var cabacLevelOffset = [...]int{0, 10, 20, 30, 39}

// DecodeResidualBlockCABAC decodes one residual block into scan order.
// codedContextIncrement is condTermFlagA + 2*condTermFlagB, already derived
// from neighbouring transform blocks by the macroblock layer.
func DecodeResidualBlockCABAC(models *CABACModels, decoder cabacBypassDecoder, category CABACBlockCategory, codedContextIncrement int) ([]int, error) {
	if int(category) >= len(cabacBlockMaximum) || codedContextIncrement < 0 || codedContextIncrement > 3 {
		return nil, fmt.Errorf("invalid CABAC residual block parameters")
	}
	maximum := cabacBlockMaximum[category]
	coefficients := make([]int, maximum)
	codedContext := 85 + cabacCodedBlockOffset[category] + codedContextIncrement
	coded, err := models.Decode(decoder, codedContext)
	if err != nil || coded == 0 {
		if err != nil {
			return nil, fmt.Errorf("coded_block_flag ctx=%d: %w", codedContext, err)
		}
		return coefficients, nil
	}

	significantPositions := make([]int, 0, maximum)
	significantBase := 105 + cabacSignificantOffset[category]
	lastBase := 166 + cabacSignificantOffset[category]
	lastFound := false
	for position := 0; position < maximum-1; position++ {
		significant, decodeErr := models.Decode(decoder, significantBase+position)
		if decodeErr != nil {
			return nil, fmt.Errorf("significant_coeff_flag position=%d ctx=%d: %w", position, significantBase+position, decodeErr)
		}
		if significant == 0 {
			continue
		}
		significantPositions = append(significantPositions, position)
		last, decodeErr := models.Decode(decoder, lastBase+position)
		if decodeErr != nil {
			return nil, fmt.Errorf("last_significant_coeff_flag position=%d ctx=%d: %w", position, lastBase+position, decodeErr)
		}
		if last != 0 {
			lastFound = true
			break
		}
	}
	if !lastFound {
		significantPositions = append(significantPositions, maximum-1)
	}

	numEqualOne, numGreaterOne := 0, 0
	levelBase := 227 + cabacLevelOffset[category]
	for index := len(significantPositions) - 1; index >= 0; index-- {
		minusOne, decodeErr := decodeCABACCoefficientMagnitude(models, decoder, levelBase, numEqualOne, numGreaterOne)
		if decodeErr != nil {
			return nil, fmt.Errorf("coeff_abs_level_minus1 reverse-index=%d: %w", index, decodeErr)
		}
		absolute := minusOne + 1
		sign, decodeErr := decoder.DecodeBypass()
		if decodeErr != nil {
			return nil, fmt.Errorf("coeff_sign_flag reverse-index=%d: %w", index, decodeErr)
		}
		level := absolute
		if sign != 0 {
			level = -level
		}
		coefficients[significantPositions[index]] = level
		if absolute == 1 {
			numEqualOne++
		} else {
			numGreaterOne++
		}
	}
	return coefficients, nil
}

func decodeCABACCoefficientMagnitude(models *CABACModels, decoder cabacBypassDecoder, base, numEqualOne, numGreaterOne int) (int, error) {
	value := 0
	for binIndex := 0; binIndex < 14; binIndex++ {
		increment := 5 + minInt(4, numGreaterOne)
		if binIndex == 0 {
			if numGreaterOne != 0 {
				increment = 0
			} else {
				increment = minInt(4, 1+numEqualOne)
			}
		}
		bin, err := models.Decode(decoder, base+increment)
		if err != nil {
			return 0, err
		}
		if bin == 0 {
			return value, nil
		}
		value++
	}
	suffix, err := decodeCABACUEGSuffix(decoder, 0)
	if err != nil {
		return 0, err
	}
	return value + suffix, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// DecodeIntra4x4LumaResidualCABAC groups sixteen CABAC blocks into the same
// representation consumed by the existing inverse-transform pipeline.
func DecodeIntra4x4LumaResidualCABAC(models *CABACModels, decoder cabacBypassDecoder, header MacroblockHeader, codedIncrements [16]int) (Intra4x4LumaResidual, error) {
	if header.Kind != MacroblockIntra4x4 && header.Kind != MacroblockInter {
		return Intra4x4LumaResidual{}, fmt.Errorf("macroblock kind %d has no 4x4 luma residual", header.Kind)
	}
	var result Intra4x4LumaResidual
	for block := range 16 {
		if header.CodedBlockPatternLuma&(1<<uint(block/4)) == 0 {
			continue
		}
		coefficients, err := DecodeResidualBlockCABAC(models, decoder, CABACLuma4x4, codedIncrements[block])
		if err != nil {
			return Intra4x4LumaResidual{}, fmt.Errorf("CABAC luma block %d: %w", block, err)
		}
		for index, coefficient := range coefficients {
			result.Blocks[block][index] = int64(coefficient)
		}
	}
	return result, nil
}

// DecodeIntra16x16LumaResidualCABAC decodes the mandatory DC block and the AC
// blocks selected by CodedBlockPatternLuma.
func DecodeIntra16x16LumaResidualCABAC(models *CABACModels, decoder cabacBypassDecoder, header MacroblockHeader, dcIncrement int, acIncrements [16]int) (Intra16x16LumaResidual, error) {
	if header.Kind != MacroblockIntra16x16 {
		return Intra16x16LumaResidual{}, fmt.Errorf("macroblock is not Intra16x16")
	}
	dc, err := DecodeResidualBlockCABAC(models, decoder, CABACLumaDC, dcIncrement)
	if err != nil {
		return Intra16x16LumaResidual{}, fmt.Errorf("CABAC Intra16x16 DC: %w", err)
	}
	var result Intra16x16LumaResidual
	for index, coefficient := range dc {
		result.DC[index] = int64(coefficient)
	}
	for block := range 16 {
		if header.CodedBlockPatternLuma&(1<<uint(block/4)) == 0 {
			continue
		}
		ac, decodeErr := DecodeResidualBlockCABAC(models, decoder, CABACLumaAC, acIncrements[block])
		if decodeErr != nil {
			return Intra16x16LumaResidual{}, fmt.Errorf("CABAC Intra16x16 AC block %d: %w", block, decodeErr)
		}
		for index, coefficient := range ac {
			result.AC[block][index] = int64(coefficient)
		}
	}
	return result, nil
}

// DecodeChromaResidual420CABAC groups the two chroma DC blocks and optional
// four AC blocks per component for 4:2:0 pictures.
func DecodeChromaResidual420CABAC(models *CABACModels, decoder cabacBypassDecoder, header MacroblockHeader, dcIncrements [2]int, acIncrements [2][4]int) (ChromaResidual420, error) {
	var result ChromaResidual420
	if header.CodedBlockPatternChroma == 0 {
		return result, nil
	}
	for component := range 2 {
		dc, err := DecodeResidualBlockCABAC(models, decoder, CABACChromaDC, dcIncrements[component])
		if err != nil {
			return ChromaResidual420{}, fmt.Errorf("CABAC chroma DC component %d: %w", component, err)
		}
		for index, coefficient := range dc {
			result.DC[component][index] = int64(coefficient)
		}
	}
	if header.CodedBlockPatternChroma != 2 {
		return result, nil
	}
	for component := range 2 {
		for block := range 4 {
			ac, err := DecodeResidualBlockCABAC(models, decoder, CABACChromaAC, acIncrements[component][block])
			if err != nil {
				return ChromaResidual420{}, fmt.Errorf("CABAC chroma AC component %d block %d: %w", component, block, err)
			}
			for index, coefficient := range ac {
				result.AC[component][block][index] = int64(coefficient)
			}
		}
	}
	return result, nil
}
