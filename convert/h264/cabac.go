package h264

import "fmt"

// CABACContext is one probability state and its most probable symbol.
type CABACContext struct {
	State uint8
	MPS   uint8
}

// NewCABACContext derives a context state from the (m,n) initialization pair.
func NewCABACContext(m, n int8, sliceQP int) CABACContext {
	pre := clipInt(((int(m)*clamp(sliceQP, 0, 51))>>4)+int(n), 1, 126)
	if pre <= 63 {
		return CABACContext{State: uint8(63 - pre)}
	}
	return CABACContext{State: uint8(pre - 64), MPS: 1}
}

// CABACDecoder implements the binary arithmetic decoder in H.264 clause 9.3.
type CABACDecoder struct {
	reader *BitReader
	rangeV uint16
	offset uint16
}

// NewCABACDecoder consumes cabac_alignment_one_bit and the initial nine-bit
// arithmetic offset.
func NewCABACDecoder(reader *BitReader) (*CABACDecoder, error) {
	if reader == nil {
		return nil, malformed("nil CABAC bit reader")
	}
	if err := reader.AlignToByteWithBit(1); err != nil {
		return nil, fmt.Errorf("CABAC alignment: %w", err)
	}
	offset, err := reader.ReadBits(9)
	if err != nil {
		return nil, malformed("CABAC initial offset is truncated")
	}
	if offset >= 510 {
		return nil, malformed("CABAC initial offset is out of range")
	}
	return &CABACDecoder{reader: reader, rangeV: 510, offset: uint16(offset)}, nil
}

// DecodeBin decodes a context-modelled bin and updates that context.
func (d *CABACDecoder) DecodeBin(context *CABACContext) (uint8, error) {
	if d == nil || d.reader == nil || context == nil || context.State > 63 || context.MPS > 1 {
		return 0, malformed("invalid CABAC decoder or context")
	}
	qRange := (d.rangeV >> 6) & 3
	lps := uint16(cabacRangeLPS[context.State][qRange])
	d.rangeV -= lps
	bin := context.MPS
	if d.offset >= d.rangeV {
		bin ^= 1
		d.offset -= d.rangeV
		d.rangeV = lps
		if context.State == 0 {
			context.MPS ^= 1
		}
		context.State = cabacTransitionLPS[context.State]
	} else {
		context.State = cabacTransitionMPS[context.State]
	}
	if err := d.renormalize(); err != nil {
		return 0, err
	}
	return bin, nil
}

// DecodeBypass decodes a bypass-coded bin without changing range or contexts.
func (d *CABACDecoder) DecodeBypass() (uint8, error) {
	bit, err := d.reader.ReadBit()
	if err != nil {
		return 0, malformed("CABAC bypass bin is truncated")
	}
	d.offset = d.offset<<1 | uint16(bit)
	if d.offset >= d.rangeV {
		d.offset -= d.rangeV
		return 1, nil
	}
	return 0, nil
}

// DecodeTerminate decodes end_of_slice_flag.
func (d *CABACDecoder) DecodeTerminate() (uint8, error) {
	d.rangeV -= 2
	if d.offset >= d.rangeV {
		return 1, nil
	}
	if err := d.renormalize(); err != nil {
		return 0, err
	}
	return 0, nil
}

func (d *CABACDecoder) renormalize() error {
	for d.rangeV < 256 {
		bit, err := d.reader.ReadBit()
		if err != nil {
			return malformed("CABAC renormalization is truncated")
		}
		d.rangeV <<= 1
		d.offset = d.offset<<1 | uint16(bit)
	}
	return nil
}

var cabacTransitionMPS = [64]uint8{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
	17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
	33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48,
	49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 62, 63,
}

var cabacTransitionLPS = [64]uint8{
	0, 0, 1, 2, 2, 4, 4, 5, 6, 7, 8, 9, 9, 11, 11, 12,
	13, 13, 15, 15, 16, 16, 18, 18, 19, 19, 21, 21, 22, 22, 23, 24,
	24, 25, 26, 26, 27, 27, 28, 29, 29, 30, 30, 30, 31, 32, 32, 33,
	33, 33, 34, 34, 35, 35, 35, 36, 36, 36, 37, 37, 37, 38, 38, 63,
}

var cabacRangeLPS = [64][4]uint8{
	{128, 176, 208, 240}, {128, 167, 197, 227}, {128, 158, 187, 216}, {123, 150, 178, 205},
	{116, 142, 169, 195}, {111, 135, 160, 185}, {105, 128, 152, 175}, {100, 122, 144, 166},
	{95, 116, 137, 158}, {90, 110, 130, 150}, {85, 104, 123, 142}, {81, 99, 117, 135},
	{77, 94, 111, 128}, {73, 89, 105, 122}, {69, 85, 100, 116}, {66, 80, 95, 110},
	{62, 76, 90, 104}, {59, 72, 86, 99}, {56, 69, 81, 94}, {53, 65, 77, 89},
	{51, 62, 73, 85}, {48, 59, 69, 80}, {46, 56, 66, 76}, {43, 53, 63, 72},
	{41, 50, 59, 69}, {39, 48, 56, 65}, {37, 45, 54, 62}, {35, 43, 51, 59},
	{33, 41, 48, 56}, {32, 39, 46, 53}, {30, 37, 43, 50}, {29, 35, 41, 48},
	{27, 33, 39, 45}, {26, 31, 37, 43}, {24, 30, 35, 41}, {23, 28, 33, 39},
	{22, 27, 32, 37}, {21, 26, 30, 35}, {20, 24, 29, 33}, {19, 23, 27, 31},
	{18, 22, 26, 30}, {17, 21, 25, 28}, {16, 20, 23, 27}, {15, 19, 22, 25},
	{14, 18, 21, 24}, {14, 17, 20, 23}, {13, 16, 19, 22}, {12, 15, 18, 21},
	{12, 14, 17, 20}, {11, 14, 16, 19}, {11, 13, 15, 18}, {10, 12, 15, 17},
	{10, 12, 14, 16}, {9, 11, 13, 15}, {9, 11, 12, 14}, {8, 10, 12, 14},
	{8, 9, 11, 13}, {7, 9, 11, 12}, {7, 9, 10, 12}, {7, 8, 10, 11},
	{6, 8, 9, 11}, {6, 7, 9, 10}, {6, 7, 8, 9}, {2, 2, 2, 2},
}
