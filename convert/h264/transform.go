package h264

import "fmt"

var zigzag4x4 = [16]uint8{0, 1, 4, 8, 5, 2, 3, 6, 9, 12, 13, 10, 7, 11, 14, 15}

var inverseLevelScale4x4 = [6][3]int64{
	{10, 13, 16},
	{11, 14, 18},
	{13, 16, 20},
	{14, 18, 23},
	{16, 20, 25},
	{18, 23, 29},
}

// InverseTransform4x4 performs inverse scanning, inverse quantisation, and the
// H.264 integer 4x4 inverse transform. Coefficients are supplied in scan order.
func InverseTransform4x4(coefficients [16]int64, qp int) ([16]int64, error) {
	if qp < 0 || qp > 87 {
		return [16]int64{}, fmt.Errorf("quantisation parameter %d is out of range", qp)
	}
	scaled, err := scale4x4(coefficients, qp)
	if err != nil {
		return [16]int64{}, err
	}
	return inverseIntegerTransform4x4(scaled), nil
}

// InverseTransform4x4AC combines an already transformed/scaled DC value with
// fifteen AC coefficients, then performs the integer inverse transform.
func InverseTransform4x4AC(ac [15]int64, scaledDC int64, qp int) ([16]int64, error) {
	var coefficients [16]int64
	copy(coefficients[1:], ac[:])
	scaled, err := scale4x4(coefficients, qp)
	if err != nil {
		return [16]int64{}, err
	}
	scaled[0][0] = scaledDC
	return inverseIntegerTransform4x4(scaled), nil
}

func scale4x4(coefficients [16]int64, qp int) ([4][4]int64, error) {
	var scaled [4][4]int64
	for scan, coefficient := range coefficients {
		position := int(zigzag4x4[scan])
		y, x := position/4, position%4
		category := 1
		if y%2 == 0 && x%2 == 0 {
			category = 0
		} else if y%2 == 1 && x%2 == 1 {
			category = 2
		}
		value := coefficient * inverseLevelScale4x4[qp%6][category]
		value <<= qp / 6
		scaled[y][x] = value
	}
	return scaled, nil
}

func inverseIntegerTransform4x4(scaled [4][4]int64) [16]int64 {
	var horizontal [4][4]int64
	for y := range 4 {
		horizontal[y] = inverseTransformLine(scaled[y])
	}
	var result [16]int64
	for x := range 4 {
		column := [4]int64{horizontal[0][x], horizontal[1][x], horizontal[2][x], horizontal[3][x]}
		column = inverseTransformLine(column)
		for y := range 4 {
			result[y*4+x] = (column[y] + 32) >> 6
		}
	}
	return result
}

func inverseTransformLine(value [4]int64) [4]int64 {
	e0 := value[0] + value[2]
	e1 := value[0] - value[2]
	e2 := (value[1] >> 1) - value[3]
	e3 := value[1] + (value[3] >> 1)
	return [4]int64{e0 + e3, e1 + e2, e1 - e2, e0 - e3}
}

// TransformIntra16x16DC performs the 4x4 inverse Hadamard transform and DC
// scaling. Its output supplies coefficient zero of each luma 4x4 block.
func TransformIntra16x16DC(coefficients [16]int64, qp int) ([16]int64, error) {
	if qp < 0 || qp > 87 {
		return [16]int64{}, fmt.Errorf("quantisation parameter %d is out of range", qp)
	}
	var input [4][4]int64
	for scan, value := range coefficients {
		position := int(zigzag4x4[scan])
		input[position/4][position%4] = value
	}
	var horizontal [4][4]int64
	for row := range 4 {
		horizontal[row] = hadamard4(input[row])
	}
	var result [16]int64
	for column := range 4 {
		values := hadamard4([4]int64{horizontal[0][column], horizontal[1][column], horizontal[2][column], horizontal[3][column]})
		for row, value := range values {
			scaled := value * inverseLevelScale4x4[qp%6][0]
			quotient := qp / 6
			if quotient >= 2 {
				scaled <<= quotient - 2
			} else {
				scaled = (scaled + int64(1<<(1-quotient))) >> (2 - quotient)
			}
			result[row*4+column] = scaled
		}
	}
	return result, nil
}

// TransformChromaDC420 performs the 2x2 inverse Hadamard and scaling.
func TransformChromaDC420(coefficients [4]int64, qp int) ([4]int64, error) {
	if qp < 0 || qp > 87 {
		return [4]int64{}, fmt.Errorf("quantisation parameter %d is out of range", qp)
	}
	c0, c1, c2, c3 := coefficients[0], coefficients[1], coefficients[2], coefficients[3]
	values := [4]int64{c0 + c1 + c2 + c3, c0 - c1 + c2 - c3, c0 + c1 - c2 - c3, c0 - c1 - c2 + c3}
	for index, value := range values {
		value *= inverseLevelScale4x4[qp%6][0]
		if quotient := qp / 6; quotient >= 1 {
			value <<= quotient - 1
		} else {
			value >>= 1
		}
		values[index] = value
	}
	return values, nil
}

// ChromaQP420 derives QP'C for 8-bit 4:2:0 video.
func ChromaQP420(lumaQP int, offset int64) (int, error) {
	index := int64(lumaQP) + offset
	if index < 0 {
		index = 0
	}
	if index > 51 {
		index = 51
	}
	mapped := [...]uint8{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 29, 30, 31, 32, 32, 33, 34, 34, 35, 35, 36, 36, 37, 37, 37, 38, 38, 38, 39, 39, 39, 39}
	return int(mapped[index]), nil
}

func hadamard4(value [4]int64) [4]int64 {
	a0, a1 := value[0]+value[3], value[1]+value[2]
	a2, a3 := value[1]-value[2], value[0]-value[3]
	return [4]int64{a0 + a1, a3 + a2, a0 - a1, a3 - a2}
}
