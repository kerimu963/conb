package h264

import "fmt"

// CAVLCBlockContext stores TotalCoeff values needed to derive nC for luma
// 4x4 blocks. It is intentionally separate from pixel reconstruction state.
type CAVLCBlockContext struct {
	macroblockWidth int
	totalCoeff      map[[2]int]int
	chromaTotal     [2]map[[2]int]int
}

func NewCAVLCBlockContext(macroblockWidth int) (*CAVLCBlockContext, error) {
	if macroblockWidth <= 0 {
		return nil, fmt.Errorf("macroblock width must be positive")
	}
	context := &CAVLCBlockContext{
		macroblockWidth: macroblockWidth,
		totalCoeff:      make(map[[2]int]int),
	}
	context.chromaTotal[0] = make(map[[2]int]int)
	context.chromaTotal[1] = make(map[[2]int]int)
	return context, nil
}

// Intra16x16LumaResidual keeps the DC block and the 15 AC scan positions for
// each of the sixteen luma blocks separate until inverse transform.
type Intra16x16LumaResidual struct {
	DC [16]int64
	AC [16][15]int64
}

func DecodeIntra16x16LumaResidual(
	r *BitReader,
	header MacroblockHeader,
	context *CAVLCBlockContext,
	macroblockX, macroblockY int,
) (Intra16x16LumaResidual, error) {
	if r == nil || context == nil || header.Kind != MacroblockIntra16x16 {
		return Intra16x16LumaResidual{}, malformed("invalid Intra16x16 residual input")
	}
	if macroblockX < 0 || macroblockX >= context.macroblockWidth || macroblockY < 0 {
		return Intra16x16LumaResidual{}, malformed("invalid Intra16x16 macroblock coordinate")
	}
	// For Intra16x16 DC, clause 9.2.1 maps the DC block index back to
	// luma4x4BlkIdx 0 before deriving nC.  It therefore uses the ordinary
	// left/top luma coefficient cache, not a separate history of DC blocks.
	nC, err := context.NC(macroblockX, macroblockY, 0)
	if err != nil {
		return Intra16x16LumaResidual{}, err
	}
	dc, err := DecodeResidualBlockCAVLC(r, nC, 16)
	if err != nil {
		return Intra16x16LumaResidual{}, fmt.Errorf("Intra16x16 DC: %w", err)
	}
	var result Intra16x16LumaResidual
	copy(result.DC[:], dc)
	for block := 0; block < 16; block++ {
		total := 0
		if header.CodedBlockPatternLuma&(1<<uint(block/4)) != 0 {
			nC, err = context.NC(macroblockX, macroblockY, block)
			if err != nil {
				return Intra16x16LumaResidual{}, err
			}
			ac, decodeErr := DecodeResidualBlockCAVLC(r, nC, 15)
			if decodeErr != nil {
				return Intra16x16LumaResidual{}, fmt.Errorf("Intra16x16 AC block %d: %w", block, decodeErr)
			}
			copy(result.AC[block][:], ac)
			total = countNonZero(ac)
		}
		if err = context.setTotalCoeff(macroblockX, macroblockY, block, total); err != nil {
			return Intra16x16LumaResidual{}, err
		}
	}
	return result, nil
}

// ChromaResidual420 contains 2x2 DC and four 4x4 AC blocks for Cb and Cr.
type ChromaResidual420 struct {
	DC [2][4]int64
	AC [2][4][15]int64
}

func DecodeChromaResidual420(
	r *BitReader,
	header MacroblockHeader,
	context *CAVLCBlockContext,
	macroblockX, macroblockY int,
) (ChromaResidual420, error) {
	if r == nil || context == nil || macroblockX < 0 || macroblockX >= context.macroblockWidth || macroblockY < 0 {
		return ChromaResidual420{}, malformed("invalid chroma residual input")
	}
	var result ChromaResidual420
	if header.CodedBlockPatternChroma == 0 {
		for component := range 2 {
			for block := range 4 {
				context.setChromaTotal(component, macroblockX, macroblockY, block, 0)
			}
		}
		return result, nil
	}
	for component := range 2 {
		dc, err := DecodeResidualBlockCAVLC(r, -1, 4)
		if err != nil {
			return ChromaResidual420{}, fmt.Errorf("chroma DC component %d: %w", component, err)
		}
		copy(result.DC[component][:], dc)
	}
	for component := range 2 {
		for block := range 4 {
			total := 0
			if header.CodedBlockPatternChroma == 2 {
				nC, err := context.chromaNC(component, macroblockX, macroblockY, block)
				if err != nil {
					return ChromaResidual420{}, err
				}
				position := r.Position()
				ac, decodeErr := DecodeResidualBlockCAVLC(r, nC, 15)
				if decodeErr != nil {
					left, hasLeft, top, hasTop := context.chromaNeighbourTotals(component, macroblockX, macroblockY, block)
					return ChromaResidual420{}, fmt.Errorf("chroma AC macroblock=(%d,%d) component=%d block=%d nC=%d neighbours=(%d/%t,%d/%t) bit=%d dc=%v: %w",
						macroblockX, macroblockY, component, block, nC, left, hasLeft, top, hasTop, position, result.DC, decodeErr)
				}
				copy(result.AC[component][block][:], ac)
				total = countNonZero(ac)
			}
			if err := context.setChromaTotal(component, macroblockX, macroblockY, block, total); err != nil {
				return ChromaResidual420{}, err
			}
		}
	}
	return result, nil
}

func (c *CAVLCBlockContext) chromaNC(component, macroblockX, macroblockY, block int) (int, error) {
	x, y, err := c.chromaBlockPosition(component, macroblockX, macroblockY, block)
	if err != nil {
		return 0, err
	}
	left, hasLeft := c.chromaTotal[component][[2]int{x - 1, y}]
	top, hasTop := c.chromaTotal[component][[2]int{x, y - 1}]
	return combineNC(left, hasLeft, top, hasTop), nil
}

func (c *CAVLCBlockContext) chromaNeighbourTotals(component, macroblockX, macroblockY, block int) (int, bool, int, bool) {
	x, y, _ := c.chromaBlockPosition(component, macroblockX, macroblockY, block)
	left, hasLeft := c.chromaTotal[component][[2]int{x - 1, y}]
	top, hasTop := c.chromaTotal[component][[2]int{x, y - 1}]
	return left, hasLeft, top, hasTop
}

func (c *CAVLCBlockContext) setChromaTotal(component, macroblockX, macroblockY, block, total int) error {
	x, y, err := c.chromaBlockPosition(component, macroblockX, macroblockY, block)
	if err != nil || total < 0 || total > 15 {
		if err != nil {
			return err
		}
		return malformed("chroma TotalCoeff is out of range")
	}
	c.chromaTotal[component][[2]int{x, y}] = total
	return nil
}

func (c *CAVLCBlockContext) chromaBlockPosition(component, macroblockX, macroblockY, block int) (int, int, error) {
	if c == nil || component < 0 || component > 1 || macroblockX < 0 || macroblockX >= c.macroblockWidth || macroblockY < 0 || block < 0 || block >= 4 {
		return 0, 0, malformed("invalid 4:2:0 chroma block coordinate")
	}
	return macroblockX*2 + block%2, macroblockY*2 + block/2, nil
}

func neighbouringNC(values map[[2]int]int, x, y int) int {
	left, hasLeft := values[[2]int{x - 1, y}]
	top, hasTop := values[[2]int{x, y - 1}]
	return combineNC(left, hasLeft, top, hasTop)
}

func combineNC(left int, hasLeft bool, top int, hasTop bool) int {
	switch {
	case hasLeft && hasTop:
		return (left + top + 1) / 2
	case hasLeft:
		return left
	case hasTop:
		return top
	default:
		return 0
	}
}

func countNonZero(values []int64) int {
	total := 0
	for _, value := range values {
		if value != 0 {
			total++
		}
	}
	return total
}

// NC derives the coeff_token context from available left and top blocks.
func (c *CAVLCBlockContext) NC(macroblockX, macroblockY, block int) (int, error) {
	x, y, err := c.blockPosition(macroblockX, macroblockY, block)
	if err != nil {
		return 0, err
	}
	left, hasLeft := c.totalCoeff[[2]int{x - 1, y}]
	top, hasTop := c.totalCoeff[[2]int{x, y - 1}]
	return combineNC(left, hasLeft, top, hasTop), nil
}

// Intra4x4LumaResidual contains coefficients in scan order for all luma blocks.
type Intra4x4LumaResidual struct {
	Blocks [16][16]int64
}

// DecodeIntra4x4LumaResidual decodes all coded luma blocks of an I_NxN
// macroblock and updates neighbour context for subsequent blocks.
func DecodeIntra4x4LumaResidual(
	r *BitReader,
	header MacroblockHeader,
	context *CAVLCBlockContext,
	macroblockX, macroblockY int,
) (Intra4x4LumaResidual, error) {
	if r == nil || context == nil {
		return Intra4x4LumaResidual{}, malformed("nil intra residual input")
	}
	if header.Kind != MacroblockIntra4x4 && header.Kind != MacroblockInter {
		return Intra4x4LumaResidual{}, fmt.Errorf("macroblock kind %d has no 4x4 luma residual", header.Kind)
	}
	var result Intra4x4LumaResidual
	for block := 0; block < 16; block++ {
		coded := header.CodedBlockPatternLuma&(1<<uint(block/4)) != 0
		total := 0
		if coded {
			nC, err := context.NC(macroblockX, macroblockY, block)
			if err != nil {
				return Intra4x4LumaResidual{}, err
			}
			coefficients, err := DecodeResidualBlockCAVLC(r, nC, 16)
			if err != nil {
				return Intra4x4LumaResidual{}, fmt.Errorf("luma block %d: %w", block, err)
			}
			copy(result.Blocks[block][:], coefficients)
			for _, coefficient := range coefficients {
				if coefficient != 0 {
					total++
				}
			}
		}
		if err := context.setTotalCoeff(macroblockX, macroblockY, block, total); err != nil {
			return Intra4x4LumaResidual{}, err
		}
	}
	return result, nil
}

func (c *CAVLCBlockContext) setTotalCoeff(macroblockX, macroblockY, block, total int) error {
	if total < 0 || total > 16 {
		return malformed("TotalCoeff is out of range")
	}
	x, y, err := c.blockPosition(macroblockX, macroblockY, block)
	if err != nil {
		return err
	}
	c.totalCoeff[[2]int{x, y}] = total
	return nil
}

// setPCM records the normative neighbour coefficient values after I_PCM.
func (c *CAVLCBlockContext) setPCM(macroblockX, macroblockY int) error {
	for block := range 16 {
		if err := c.setTotalCoeff(macroblockX, macroblockY, block, 16); err != nil {
			return err
		}
	}
	for component := range 2 {
		for block := range 4 {
			x, y, err := c.chromaBlockPosition(component, macroblockX, macroblockY, block)
			if err != nil {
				return err
			}
			c.chromaTotal[component][[2]int{x, y}] = 16
		}
	}
	return nil
}

func (c *CAVLCBlockContext) blockPosition(macroblockX, macroblockY, block int) (int, int, error) {
	if c == nil || macroblockX < 0 || macroblockX >= c.macroblockWidth || macroblockY < 0 || block < 0 || block >= 16 {
		return 0, 0, fmt.Errorf("invalid macroblock/block coordinate (%d, %d, %d)", macroblockX, macroblockY, block)
	}
	group := block / 4
	within := block % 4
	localX := (group%2)*2 + within%2
	localY := (group/2)*2 + within/2
	return macroblockX*4 + localX, macroblockY*4 + localY, nil
}
