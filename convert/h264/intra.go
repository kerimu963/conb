package h264

import "fmt"

type Intra16Neighbours struct {
	Top, Left        [16]uint8
	TopLeft          uint8
	TopAvailable     bool
	LeftAvailable    bool
	TopLeftAvailable bool
}

type Intra4Neighbours struct {
	Top               [8]uint8
	Left              [4]uint8
	TopLeft           uint8
	TopAvailable      bool
	TopRightAvailable bool
	LeftAvailable     bool
	TopLeftAvailable  bool
}

// PredictIntra4x4 implements the nine Intra4x4 prediction modes.
func PredictIntra4x4(mode uint8, n Intra4Neighbours) ([16]uint8, error) {
	var output [16]uint8
	top := func(index int) int {
		if index < 0 {
			return int(n.TopLeft)
		}
		return int(n.Top[index])
	}
	left := func(index int) int {
		if index < 0 {
			return int(n.TopLeft)
		}
		return int(n.Left[index])
	}
	requireTop := func(right bool) error {
		if !n.TopAvailable || (right && !n.TopRightAvailable) {
			return malformed("Intra4x4 mode requires unavailable top samples")
		}
		return nil
	}
	requireBoth := func() error {
		if !n.TopAvailable || !n.LeftAvailable || !n.TopLeftAvailable {
			return malformed("Intra4x4 mode requires unavailable top/left samples")
		}
		return nil
	}
	if mode == 0 || mode == 3 || mode == 7 {
		if err := requireTop(mode != 0); err != nil {
			return output, err
		}
	} else if mode == 1 || mode == 8 {
		if !n.LeftAvailable {
			return output, malformed("Intra4x4 mode requires unavailable left samples")
		}
	} else if mode >= 4 && mode <= 6 {
		if err := requireBoth(); err != nil {
			return output, err
		}
	}
	for y := range 4 {
		for x := range 4 {
			value := 0
			switch mode {
			case 0:
				value = top(x)
			case 1:
				value = left(y)
			case 2:
				sum, count := 0, 0
				if n.TopAvailable {
					for i := range 4 {
						sum += top(i)
					}
					count += 4
				}
				if n.LeftAvailable {
					for i := range 4 {
						sum += left(i)
					}
					count += 4
				}
				if count == 0 {
					value = 128
				} else {
					value = (sum + count/2) / count
				}
			case 3:
				z := x + y
				if z == 6 {
					value = (top(6) + 3*top(7) + 2) >> 2
				} else {
					value = (top(z) + 2*top(z+1) + top(z+2) + 2) >> 2
				}
			case 4:
				if x > y {
					z := x - y
					value = (top(z-2) + 2*top(z-1) + top(z) + 2) >> 2
				} else if x < y {
					z := y - x
					value = (left(z-2) + 2*left(z-1) + left(z) + 2) >> 2
				} else {
					value = (top(0) + 2*top(-1) + left(0) + 2) >> 2
				}
			case 5:
				z := 2*x - y
				if z >= 0 && z%2 == 0 {
					value = (top(x-(y>>1)-1) + top(x-(y>>1)) + 1) >> 1
				} else if z > 0 {
					value = (top(x-(y>>1)-2) + 2*top(x-(y>>1)-1) + top(x-(y>>1)) + 2) >> 2
				} else if z == -1 {
					value = (left(0) + 2*top(-1) + top(0) + 2) >> 2
				} else {
					value = (left(y-1) + 2*left(y-2) + left(y-3) + 2) >> 2
				}
			case 6:
				z := 2*y - x
				if z >= 0 && z%2 == 0 {
					value = (left(y-(x>>1)-1) + left(y-(x>>1)) + 1) >> 1
				} else if z > 0 {
					value = (left(y-(x>>1)-2) + 2*left(y-(x>>1)-1) + left(y-(x>>1)) + 2) >> 2
				} else if z == -1 {
					value = (left(0) + 2*top(-1) + top(0) + 2) >> 2
				} else {
					value = (top(x-1) + 2*top(x-2) + top(x-3) + 2) >> 2
				}
			case 7:
				z := x + (y >> 1)
				if y%2 == 0 {
					value = (top(z) + top(z+1) + 1) >> 1
				} else {
					value = (top(z) + 2*top(z+1) + top(z+2) + 2) >> 2
				}
			case 8:
				z := x + 2*y
				if z == 0 || z == 2 || z == 4 {
					value = (left(y+(x>>1)) + left(y+(x>>1)+1) + 1) >> 1
				} else if z == 1 || z == 3 {
					value = (left(y+(x>>1)) + 2*left(y+(x>>1)+1) + left(y+(x>>1)+2) + 2) >> 2
				} else if z == 5 {
					value = (left(2) + 3*left(3) + 2) >> 2
				} else {
					value = left(3)
				}
			default:
				return output, fmt.Errorf("invalid Intra4x4 prediction mode %d", mode)
			}
			output[y*4+x] = uint8(value)
		}
	}
	return output, nil
}

// PredictIntra16x16 produces an 8-bit luma predictor for modes 0..3
// (Vertical, Horizontal, DC, Plane).
func PredictIntra16x16(mode uint8, neighbours Intra16Neighbours) ([256]uint8, error) {
	var prediction [256]uint8
	switch mode {
	case 0:
		if !neighbours.TopAvailable {
			return prediction, malformed("Intra16x16 vertical prediction requires top samples")
		}
		for y := range 16 {
			copy(prediction[y*16:(y+1)*16], neighbours.Top[:])
		}
	case 1:
		if !neighbours.LeftAvailable {
			return prediction, malformed("Intra16x16 horizontal prediction requires left samples")
		}
		for y := range 16 {
			for x := range 16 {
				prediction[y*16+x] = neighbours.Left[y]
			}
		}
	case 2:
		value := 128
		if neighbours.TopAvailable && neighbours.LeftAvailable {
			sum := 0
			for i := range 16 {
				sum += int(neighbours.Top[i]) + int(neighbours.Left[i])
			}
			value = (sum + 16) >> 5
		} else if neighbours.TopAvailable {
			sum := 0
			for _, sample := range neighbours.Top {
				sum += int(sample)
			}
			value = (sum + 8) >> 4
		} else if neighbours.LeftAvailable {
			sum := 0
			for _, sample := range neighbours.Left {
				sum += int(sample)
			}
			value = (sum + 8) >> 4
		}
		for i := range prediction {
			prediction[i] = uint8(value)
		}
	case 3:
		if !neighbours.TopAvailable || !neighbours.LeftAvailable || !neighbours.TopLeftAvailable {
			return prediction, malformed("Intra16x16 plane prediction requires top, left, and top-left samples")
		}
		horizontal, vertical := 0, 0
		for i := 1; i <= 7; i++ {
			horizontal += i * (int(neighbours.Top[7+i]) - int(neighbours.Top[7-i]))
			vertical += i * (int(neighbours.Left[7+i]) - int(neighbours.Left[7-i]))
		}
		horizontal += 8 * (int(neighbours.Top[15]) - int(neighbours.TopLeft))
		vertical += 8 * (int(neighbours.Left[15]) - int(neighbours.TopLeft))
		a := 16 * (int(neighbours.Top[15]) + int(neighbours.Left[15]))
		b, c := (5*horizontal+32)>>6, (5*vertical+32)>>6
		for y := range 16 {
			for x := range 16 {
				prediction[y*16+x] = clipByte(int64((a + b*(x-7) + c*(y-7) + 16) >> 5))
			}
		}
	default:
		return prediction, fmt.Errorf("invalid Intra16x16 prediction mode %d", mode)
	}
	return prediction, nil
}

// TransformIntra16x16Luma turns decoded DC/AC levels into sixteen spatial
// residual blocks in H.264 luma4x4BlkIdx order.
func TransformIntra16x16Luma(residual Intra16x16LumaResidual, qp int) ([16][16]int64, error) {
	dc, err := TransformIntra16x16DC(residual.DC, qp)
	if err != nil {
		return [16][16]int64{}, err
	}
	var result [16][16]int64
	for block := range 16 {
		localX, localY := lumaBlockXY(block)
		transformed, transformErr := InverseTransform4x4AC(residual.AC[block], dc[localY*4+localX], qp)
		if transformErr != nil {
			return [16][16]int64{}, transformErr
		}
		result[block] = transformed
	}
	return result, nil
}

// ReconstructIntra16x16 adds residual samples to prediction and clips to 8 bit.
func ReconstructIntra16x16(prediction [256]uint8, residual [16][16]int64) [256]uint8 {
	result := prediction
	for block := range 16 {
		blockX, blockY := lumaBlockXY(block)
		for y := range 4 {
			for x := range 4 {
				position := (blockY*4+y)*16 + blockX*4 + x
				result[position] = clipByte(int64(prediction[position]) + residual[block][y*4+x])
			}
		}
	}
	return result
}

func lumaBlockXY(block int) (int, int) {
	group, within := block/4, block%4
	return (group%2)*2 + within%2, (group/2)*2 + within/2
}

func clipByte(value int64) uint8 {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return uint8(value)
}

type ChromaNeighbours420 struct {
	Top, Left        [8]uint8
	TopLeft          uint8
	TopAvailable     bool
	LeftAvailable    bool
	TopLeftAvailable bool
}

// PredictChroma420 implements DC, Horizontal, Vertical, and Plane modes.
func PredictChroma420(mode uint64, n ChromaNeighbours420) ([64]uint8, error) {
	var result [64]uint8
	sum := func(values [8]uint8, start int) int {
		total := 0
		for i := start; i < start+4; i++ {
			total += int(values[i])
		}
		return total
	}
	switch mode {
	case 0:
		top0, top1, left0, left1 := sum(n.Top, 0), sum(n.Top, 4), sum(n.Left, 0), sum(n.Left, 4)
		values := [4]int{128, 128, 128, 128}
		if n.TopAvailable && n.LeftAvailable {
			values = [4]int{(top0 + left0 + 4) >> 3, (top1 + 2) >> 2, (left1 + 2) >> 2, (top1 + left1 + 4) >> 3}
		} else if n.TopAvailable {
			values = [4]int{(top0 + 2) >> 2, (top1 + 2) >> 2, (top0 + 2) >> 2, (top1 + 2) >> 2}
		} else if n.LeftAvailable {
			values = [4]int{(left0 + 2) >> 2, (left0 + 2) >> 2, (left1 + 2) >> 2, (left1 + 2) >> 2}
		}
		for y := range 8 {
			for x := range 8 {
				result[y*8+x] = uint8(values[(y/4)*2+x/4])
			}
		}
	case 1:
		if !n.LeftAvailable {
			return result, malformed("chroma horizontal prediction requires left samples")
		}
		for y := range 8 {
			for x := range 8 {
				result[y*8+x] = n.Left[y]
			}
		}
	case 2:
		if !n.TopAvailable {
			return result, malformed("chroma vertical prediction requires top samples")
		}
		for y := range 8 {
			copy(result[y*8:(y+1)*8], n.Top[:])
		}
	case 3:
		if !n.TopAvailable || !n.LeftAvailable || !n.TopLeftAvailable {
			return result, malformed("chroma plane prediction requires all neighbours")
		}
		horizontal, vertical := 0, 0
		for i := 0; i < 4; i++ {
			topLow, leftLow := int(n.TopLeft), int(n.TopLeft)
			if i == 3 {
				topLow, leftLow = int(n.TopLeft), int(n.TopLeft)
			} else {
				topLow, leftLow = int(n.Top[2-i]), int(n.Left[2-i])
			}
			horizontal += (i + 1) * (int(n.Top[4+i]) - topLow)
			vertical += (i + 1) * (int(n.Left[4+i]) - leftLow)
		}
		a := 16 * (int(n.Top[7]) + int(n.Left[7]))
		b, c := (17*horizontal+16)>>5, (17*vertical+16)>>5
		for y := range 8 {
			for x := range 8 {
				result[y*8+x] = clipByte(int64((a + b*(x-3) + c*(y-3) + 16) >> 5))
			}
		}
	default:
		return result, fmt.Errorf("invalid chroma prediction mode %d", mode)
	}
	return result, nil
}

func TransformChromaResidual420(residual ChromaResidual420, qp [2]int) ([2][4][16]int64, error) {
	var result [2][4][16]int64
	for component := range 2 {
		dc, err := TransformChromaDC420(residual.DC[component], qp[component])
		if err != nil {
			return result, err
		}
		for block := range 4 {
			transformed, transformErr := InverseTransform4x4AC(residual.AC[component][block], dc[block], qp[component])
			if transformErr != nil {
				return result, transformErr
			}
			result[component][block] = transformed
		}
	}
	return result, nil
}

func ReconstructChroma420(prediction [64]uint8, residual [4][16]int64) [64]uint8 {
	result := prediction
	for block := range 4 {
		blockX, blockY := block%2, block/2
		for y := range 4 {
			for x := range 4 {
				position := (blockY*4+y)*8 + blockX*4 + x
				result[position] = clipByte(int64(prediction[position]) + residual[block][y*4+x])
			}
		}
	}
	return result
}
