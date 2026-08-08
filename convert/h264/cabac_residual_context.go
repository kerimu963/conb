package h264

import "fmt"

type cabacResidualMacroblock struct {
	sliceID       int
	kind          MacroblockKind
	skipped       bool
	lumaPattern   uint8
	chromaPattern uint8
}

// CABACResidualContext stores picture-level coded_block_flag state. It is
// separate from CAVLC's TotalCoeff context because CABAC derives probability
// contexts from transform-block presence and coded flags instead of nC.
type CABACResidualContext struct {
	macroblockWidth int
	macroblocks     map[[2]int]cabacResidualMacroblock
	lumaDC          map[[2]int]bool
	luma            map[[2]int]bool
	chromaDC        [2]map[[2]int]bool
	chroma          [2]map[[2]int]bool
}

func NewCABACResidualContext(macroblockWidth int) (*CABACResidualContext, error) {
	if macroblockWidth <= 0 {
		return nil, fmt.Errorf("CABAC macroblock width must be positive")
	}
	context := &CABACResidualContext{
		macroblockWidth: macroblockWidth,
		macroblocks:     make(map[[2]int]cabacResidualMacroblock),
		lumaDC:          make(map[[2]int]bool),
		luma:            make(map[[2]int]bool),
	}
	for component := range 2 {
		context.chromaDC[component] = make(map[[2]int]bool)
		context.chroma[component] = make(map[[2]int]bool)
	}
	return context, nil
}

// BeginMacroblock registers syntax that determines transform-block presence.
// It must be called before decoding any residual block of the macroblock.
func (c *CABACResidualContext) BeginMacroblock(mbX, mbY, sliceID int, header MacroblockHeader, skipped bool) error {
	if c == nil || mbX < 0 || mbX >= c.macroblockWidth || mbY < 0 || sliceID < 0 {
		return malformed("invalid CABAC residual macroblock coordinate")
	}
	key := [2]int{mbX, mbY}
	if _, exists := c.macroblocks[key]; exists {
		return malformed("CABAC residual macroblock is duplicated")
	}
	c.macroblocks[key] = cabacResidualMacroblock{
		sliceID: sliceID, kind: header.Kind, skipped: skipped,
		lumaPattern: header.CodedBlockPatternLuma, chromaPattern: header.CodedBlockPatternChroma,
	}
	return nil
}

// DecodeBlock derives coded_block_flag's context increment, decodes the block,
// and records its coded flag for following left/top neighbours.
func (c *CABACResidualContext) DecodeBlock(models *CABACModels, decoder cabacBypassDecoder, category CABACBlockCategory, mbX, mbY, block, component int) ([]int, error) {
	increment, err := c.CodedBlockIncrement(category, mbX, mbY, block, component)
	if err != nil {
		return nil, err
	}
	coefficients, err := DecodeResidualBlockCABAC(models, decoder, category, increment)
	if err != nil {
		return nil, err
	}
	coded := false
	for _, coefficient := range coefficients {
		coded = coded || coefficient != 0
	}
	if err = c.record(category, mbX, mbY, block, component, coded); err != nil {
		return nil, err
	}
	return coefficients, nil
}

// CodedBlockIncrement returns condTermFlagA + 2*condTermFlagB.
func (c *CABACResidualContext) CodedBlockIncrement(category CABACBlockCategory, mbX, mbY, block, component int) (int, error) {
	if c == nil {
		return 0, malformed("nil CABAC residual context")
	}
	current, ok := c.macroblocks[[2]int{mbX, mbY}]
	if !ok || int(category) >= len(cabacBlockMaximum) {
		return 0, malformed("CABAC residual macroblock has not begun")
	}
	validBlock := block == 0
	if category == CABACLumaAC || category == CABACLuma4x4 {
		validBlock = block >= 0 && block < 16
	} else if category == CABACChromaAC {
		validBlock = block >= 0 && block < 4
	}
	if component < 0 || component > 1 || !validBlock {
		return 0, malformed("invalid CABAC residual block coordinate")
	}
	left, top, err := c.neighbours(category, mbX, mbY, block, component)
	if err != nil {
		return 0, err
	}
	currentIntra := current.kind == MacroblockIntra4x4 || current.kind == MacroblockIntra16x16
	return c.condition(left, current.sliceID, currentIntra, category, component) +
		2*c.condition(top, current.sliceID, currentIntra, category, component), nil
}

type cabacTransformLocation struct {
	mbX, mbY int
	blockX   int
	blockY   int
	valid    bool
}

func (c *CABACResidualContext) neighbours(category CABACBlockCategory, mbX, mbY, block, component int) (cabacTransformLocation, cabacTransformLocation, error) {
	switch category {
	case CABACLumaDC, CABACChromaDC:
		return cabacTransformLocation{mbX: mbX - 1, mbY: mbY, valid: mbX > 0},
			cabacTransformLocation{mbX: mbX, mbY: mbY - 1, valid: mbY > 0}, nil
	case CABACLumaAC, CABACLuma4x4:
		if block < 0 || block >= 16 {
			return cabacTransformLocation{}, cabacTransformLocation{}, malformed("invalid CABAC luma block")
		}
		localX, localY := lumaBlockXY(block)
		globalX, globalY := mbX*4+localX, mbY*4+localY
		return c.lumaLocation(globalX-1, globalY), c.lumaLocation(globalX, globalY-1), nil
	case CABACChromaAC:
		if block < 0 || block >= 4 {
			return cabacTransformLocation{}, cabacTransformLocation{}, malformed("invalid CABAC chroma block")
		}
		globalX, globalY := mbX*2+block%2, mbY*2+block/2
		return c.chromaLocation(globalX-1, globalY), c.chromaLocation(globalX, globalY-1), nil
	default:
		return cabacTransformLocation{}, cabacTransformLocation{}, malformed("invalid CABAC block category")
	}
}

func (c *CABACResidualContext) lumaLocation(x, y int) cabacTransformLocation {
	if x < 0 || y < 0 {
		return cabacTransformLocation{}
	}
	return cabacTransformLocation{mbX: x / 4, mbY: y / 4, blockX: x, blockY: y, valid: x/4 < c.macroblockWidth}
}

func (c *CABACResidualContext) chromaLocation(x, y int) cabacTransformLocation {
	if x < 0 || y < 0 {
		return cabacTransformLocation{}
	}
	return cabacTransformLocation{mbX: x / 2, mbY: y / 2, blockX: x, blockY: y, valid: x/2 < c.macroblockWidth}
}

func (c *CABACResidualContext) condition(location cabacTransformLocation, sliceID int, currentIntra bool, category CABACBlockCategory, component int) int {
	if !location.valid {
		if currentIntra {
			return 1
		}
		return 0
	}
	state, available := c.macroblocks[[2]int{location.mbX, location.mbY}]
	if !available || state.sliceID != sliceID {
		if currentIntra {
			return 1
		}
		return 0
	}
	if state.kind == MacroblockPCM {
		return 1
	}
	if !cabacTransformPresent(state, category, location.blockX, location.blockY) {
		return 0
	}
	var coded bool
	switch category {
	case CABACLumaDC:
		coded = c.lumaDC[[2]int{location.mbX, location.mbY}]
	case CABACLumaAC, CABACLuma4x4:
		coded = c.luma[[2]int{location.blockX, location.blockY}]
	case CABACChromaDC:
		coded = c.chromaDC[component][[2]int{location.mbX, location.mbY}]
	case CABACChromaAC:
		coded = c.chroma[component][[2]int{location.blockX, location.blockY}]
	}
	if coded {
		return 1
	}
	return 0
}

func cabacTransformPresent(state cabacResidualMacroblock, category CABACBlockCategory, globalX, globalY int) bool {
	if state.skipped || state.kind == MacroblockPCM {
		return false
	}
	switch category {
	case CABACLumaDC:
		return state.kind == MacroblockIntra16x16
	case CABACLumaAC, CABACLuma4x4:
		localX, localY := globalX%4, globalY%4
		group := localY/2*2 + localX/2
		return state.lumaPattern&(1<<uint(group)) != 0
	case CABACChromaDC:
		return state.chromaPattern != 0
	case CABACChromaAC:
		return state.chromaPattern == 2
	}
	return false
}

func (c *CABACResidualContext) record(category CABACBlockCategory, mbX, mbY, block, component int, coded bool) error {
	switch category {
	case CABACLumaDC:
		c.lumaDC[[2]int{mbX, mbY}] = coded
	case CABACLumaAC, CABACLuma4x4:
		localX, localY := lumaBlockXY(block)
		c.luma[[2]int{mbX*4 + localX, mbY*4 + localY}] = coded
	case CABACChromaDC:
		c.chromaDC[component][[2]int{mbX, mbY}] = coded
	case CABACChromaAC:
		c.chroma[component][[2]int{mbX*2 + block%2, mbY*2 + block/2}] = coded
	default:
		return malformed("invalid CABAC block category")
	}
	return nil
}

func (c *CABACResidualContext) DecodeLuma4x4(models *CABACModels, decoder cabacBypassDecoder, header MacroblockHeader, mbX, mbY int) (Intra4x4LumaResidual, error) {
	var result Intra4x4LumaResidual
	for block := range 16 {
		if header.CodedBlockPatternLuma&(1<<uint(block/4)) == 0 {
			continue
		}
		coefficients, err := c.DecodeBlock(models, decoder, CABACLuma4x4, mbX, mbY, block, 0)
		if err != nil {
			return Intra4x4LumaResidual{}, fmt.Errorf("CABAC luma block %d: %w", block, err)
		}
		for index, coefficient := range coefficients {
			result.Blocks[block][index] = int64(coefficient)
		}
	}
	return result, nil
}

func (c *CABACResidualContext) DecodeLuma16x16(models *CABACModels, decoder cabacBypassDecoder, header MacroblockHeader, mbX, mbY int) (Intra16x16LumaResidual, error) {
	dc, err := c.DecodeBlock(models, decoder, CABACLumaDC, mbX, mbY, 0, 0)
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
		ac, decodeErr := c.DecodeBlock(models, decoder, CABACLumaAC, mbX, mbY, block, 0)
		if decodeErr != nil {
			return Intra16x16LumaResidual{}, fmt.Errorf("CABAC Intra16x16 AC block %d: %w", block, decodeErr)
		}
		for index, coefficient := range ac {
			result.AC[block][index] = int64(coefficient)
		}
	}
	return result, nil
}

func (c *CABACResidualContext) DecodeChroma420(models *CABACModels, decoder cabacBypassDecoder, header MacroblockHeader, mbX, mbY int) (ChromaResidual420, error) {
	var result ChromaResidual420
	if header.CodedBlockPatternChroma == 0 {
		return result, nil
	}
	for component := range 2 {
		dc, err := c.DecodeBlock(models, decoder, CABACChromaDC, mbX, mbY, 0, component)
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
			ac, err := c.DecodeBlock(models, decoder, CABACChromaAC, mbX, mbY, block, component)
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
