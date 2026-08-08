package h264

import (
	"fmt"
	"io"
)

// BitReader reads H.264 RBSP syntax elements MSB first.
type BitReader struct {
	data   []byte
	bitPos int
}

// NewBitReader removes emulation-prevention bytes from EBSP data.
func NewBitReader(ebsp []byte) (*BitReader, error) {
	rbsp := make([]byte, 0, len(ebsp))
	zeros := 0
	for i, value := range ebsp {
		if zeros >= 2 && value == 3 {
			if i+1 >= len(ebsp) || ebsp[i+1] > 3 {
				return nil, malformed("invalid emulation-prevention byte")
			}
			zeros = 0
			continue
		}
		rbsp = append(rbsp, value)
		if value == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return &BitReader{data: rbsp}, nil
}

func (r *BitReader) BitsRemaining() int { return len(r.data)*8 - r.bitPos }

// Position returns the number of consumed RBSP bits.
func (r *BitReader) Position() int { return r.bitPos }

// AlignToByte consumes pcm_alignment_zero_bit syntax and rejects non-zero
// alignment bits.
func (r *BitReader) AlignToByte() error {
	return r.AlignToByteWithBit(0)
}

// AlignToByteWithBit consumes alignment bits having the required value.
func (r *BitReader) AlignToByteWithBit(expected uint8) error {
	if expected > 1 {
		return malformed("invalid alignment bit value")
	}
	for r.bitPos%8 != 0 {
		bit, err := r.ReadBit()
		if err != nil {
			return err
		}
		if bit != expected {
			return malformed("unexpected alignment bit")
		}
	}
	return nil
}

// ReadByte reads one byte at a byte-aligned position.
func (r *BitReader) ReadByte() (byte, error) {
	if r.bitPos%8 != 0 {
		return 0, malformed("byte read is not aligned")
	}
	value, err := r.ReadBits(8)
	return byte(value), err
}

// SkipBits advances without decoding a value.
func (r *BitReader) SkipBits(count int) error {
	if count < 0 || count > r.BitsRemaining() {
		return io.ErrUnexpectedEOF
	}
	r.bitPos += count
	return nil
}

// MoreRBSPData reports whether syntax data remains before rbsp_trailing_bits.
func (r *BitReader) MoreRBSPData() bool {
	remaining := r.BitsRemaining()
	if remaining <= 0 {
		return false
	}
	// rbsp_trailing_bits is one stop bit followed only by zero alignment bits.
	for offset := 0; offset < remaining; offset++ {
		bit := r.data[(r.bitPos+offset)/8] >> (7 - (r.bitPos+offset)%8) & 1
		if offset == 0 {
			if bit == 0 {
				return true
			}
		} else if bit != 0 {
			return true
		}
	}
	return false
}

func (r *BitReader) ReadBit() (uint8, error) {
	value, err := r.ReadBits(1)
	return uint8(value), err
}

func (r *BitReader) ReadBits(count uint) (uint64, error) {
	if count > 64 {
		return 0, fmt.Errorf("cannot read %d bits at once", count)
	}
	if int(count) > r.BitsRemaining() {
		return 0, io.ErrUnexpectedEOF
	}
	var result uint64
	for range count {
		result = result<<1 | uint64(r.data[r.bitPos/8]>>(7-r.bitPos%8)&1)
		r.bitPos++
	}
	return result, nil
}

// ReadUE reads an unsigned Exp-Golomb value.
func (r *BitReader) ReadUE() (uint64, error) {
	leadingZeros := uint(0)
	for {
		bit, err := r.ReadBit()
		if err != nil {
			return 0, err
		}
		if bit == 1 {
			break
		}
		leadingZeros++
		if leadingZeros >= 64 {
			return 0, malformed("Exp-Golomb value overflows uint64")
		}
	}
	suffix, err := r.ReadBits(leadingZeros)
	if err != nil {
		return 0, err
	}
	return (uint64(1)<<leadingZeros - 1) + suffix, nil
}

// ReadSE reads a signed Exp-Golomb value.
func (r *BitReader) ReadSE() (int64, error) {
	code, err := r.ReadUE()
	if err != nil {
		return 0, err
	}
	if code&1 == 0 {
		if code/2 > uint64(1<<63) {
			return 0, malformed("signed Exp-Golomb value overflows int64")
		}
		return -int64(code / 2), nil
	}
	if code/2 > uint64(1<<63-1) {
		return 0, malformed("signed Exp-Golomb value overflows int64")
	}
	return int64(code/2 + 1), nil
}
