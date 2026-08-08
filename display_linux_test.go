//go:build linux

package conb

import "testing"

func TestScaleToMask(t *testing.T) {
	tests := []struct {
		value byte
		mask  uint32
		want  uint32
	}{
		{0, 0x00ff0000, 0},
		{255, 0x00ff0000, 0x00ff0000},
		{128, 0x0000f800, 0x00007800},
		{255, 0, 0},
	}
	for _, test := range tests {
		if got := scaleToMask(test.value, test.mask); got != test.want {
			t.Errorf("scaleToMask(%d, %#x) = %#x, want %#x", test.value, test.mask, got, test.want)
		}
	}
}

func TestAligned(t *testing.T) {
	for _, test := range []struct{ value, alignment, want int }{
		{0, 4, 0},
		{1, 4, 4},
		{4, 4, 4},
		{5, 4, 8},
		{17, 8, 24},
	} {
		if got := aligned(test.value, test.alignment); got != test.want {
			t.Errorf("aligned(%d, %d) = %d, want %d", test.value, test.alignment, got, test.want)
		}
	}
}
