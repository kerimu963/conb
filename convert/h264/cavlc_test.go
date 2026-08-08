package h264

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestEveryVariableCoeffTokenTableEntry(t *testing.T) {
	contexts := []int{0, 2, 4}
	for table, nC := range contexts {
		for trailing := 0; trailing <= 3; trailing++ {
			for total := 1; total <= 16; total++ {
				size := coeffTokenSizes[table][trailing][total-1]
				if size == 0 || trailing > total {
					continue
				}
				bits := fmt.Sprintf("%0*b", int(size), coeffTokenCodes[table][trailing][total-1])
				got, err := DecodeCoeffToken(readerFromBits(bits), nC)
				want := CoeffToken{TotalCoeff: total, TrailingOnes: trailing}
				if err != nil || got != want {
					t.Errorf("nC=%d bits=%s: got (%+v, %v), want %+v", nC, bits, got, err, want)
				}
			}
		}
	}
}

func TestEveryTotalZerosTableEntry(t *testing.T) {
	for total := 1; total < 16; total++ {
		start := totalZeroOffsets[total-1]
		for zeros := 0; zeros <= 16-total; zeros++ {
			bits := fmt.Sprintf("%0*b", int(totalZeroSizes[start+zeros]), totalZeroCodes[start+zeros])
			got, err := DecodeTotalZeros(readerFromBits(bits), total, 16)
			if err != nil || got != zeros {
				t.Errorf("total=%d bits=%s: got (%d, %v), want %d", total, bits, got, err, zeros)
			}
		}
	}
}

func TestDecodeCoeffTokenSupportedContexts(t *testing.T) {
	r := readerFromBits("001011")
	token, err := DecodeCoeffToken(r, 8)
	if err != nil || token != (CoeffToken{TotalCoeff: 3, TrailingOnes: 3}) {
		t.Fatalf("fixed coeff token = (%+v, %v)", token, err)
	}

	r = readerFromBits("000101")
	token, err = DecodeCoeffToken(r, -1)
	if err != nil || token != (CoeffToken{TotalCoeff: 3, TrailingOnes: 3}) {
		t.Fatalf("chroma DC coeff token = (%+v, %v)", token, err)
	}

	r = readerFromBits("00011")
	token, err = DecodeCoeffToken(r, 0)
	if err != nil || token != (CoeffToken{TotalCoeff: 3, TrailingOnes: 3}) {
		t.Fatalf("nC=0 coeff token = (%+v, %v)", token, err)
	}

	r = readerFromBits("1")
	position := r.Position()
	if _, err = DecodeCoeffToken(r, -2); !errors.Is(err, ErrCAVLCContextUnsupported) || r.Position() != position {
		t.Fatalf("unsupported context = (error %v, position %d), want unchanged %d", err, r.Position(), position)
	}
}

func TestDecodeTotalZerosAndResidualBlock(t *testing.T) {
	zeros, err := DecodeTotalZeros(readerFromBits("110"), 3, 16)
	if err != nil || zeros != 2 {
		t.Fatalf("total_zeros = (%d, %v), want 2", zeros, err)
	}

	// token(TotalCoeff=3, TrailingOnes=2), signs, remaining level +2,
	// total_zeros=2, then run_before values 1 and 0.
	r := readerFromBits("001010" + "01" + "1" + "110" + "01" + "1")
	coefficients, err := DecodeResidualBlockCAVLC(r, 8, 16)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]int64, 16)
	want[1], want[2], want[4] = 2, -1, 1
	if !reflect.DeepEqual(coefficients, want) {
		t.Fatalf("coefficients = %v, want %v", coefficients, want)
	}

	zeroBlock, err := DecodeResidualBlockCAVLC(readerFromBits("1"), 0, 16)
	if err != nil || !reflect.DeepEqual(zeroBlock, make([]int64, 16)) {
		t.Fatalf("zero block = (%v, %v)", zeroBlock, err)
	}
}

func TestDecodeLevels(t *testing.T) {
	// Trailing signs +1, -1 followed by level_prefix 0. The first non-trailing
	// level receives the specified +2 adjustment and decodes to +2.
	r := readerFromBits("011")
	levels, err := DecodeLevels(r, CoeffToken{TotalCoeff: 3, TrailingOnes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(levels, []int64{1, -1, 2}) {
		t.Fatalf("levels = %v, want [1 -1 2]", levels)
	}
}

func TestDecodeRunBefore(t *testing.T) {
	tests := []struct {
		zeros int
		bits  string
		want  int
	}{
		{1, "1", 0},
		{2, "00", 2},
		{5, "010", 3},
		{6, "100", 6},
		{8, "00001", 8},
	}
	for _, test := range tests {
		got, err := DecodeRunBefore(readerFromBits(test.bits), test.zeros)
		if err != nil || got != test.want {
			t.Errorf("DecodeRunBefore(zeros=%d, bits=%s) = (%d, %v), want %d", test.zeros, test.bits, got, err, test.want)
		}
	}
}

func readerFromBits(bits string) *BitReader {
	data := make([]byte, (len(bits)+7)/8)
	for i, bit := range []byte(bits) {
		if bit == '1' {
			data[i/8] |= 1 << (7 - i%8)
		}
	}
	r, err := NewBitReader(data)
	if err != nil {
		panic(err)
	}
	return r
}
