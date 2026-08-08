package h264

import (
	"testing"

	"conb"
)

func TestFrame420DrawCanvas(t *testing.T) {
	frame, _ := NewFrame420(2, 2)
	for i := range frame.Y {
		frame.Y[i] = 16
	}
	frame.Cb[0], frame.Cr[0] = 128, 128
	canvas, _ := conb.NewCanvas(2, 2)
	if err := frame.DrawCanvas(canvas); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(canvas.Pixels()); i += 4 {
		if got := canvas.Pixels()[i : i+4]; got[0] != 0 || got[1] != 0 || got[2] != 0 || got[3] != 255 {
			t.Fatalf("pixel = %v, want black", got)
		}
	}
}

func TestDrawCanvasUsesDisplayCrop(t *testing.T) {
	frame, _ := NewFrame420(4, 4)
	for i := range frame.Y {
		frame.Y[i] = uint8(16 + i)
	}
	for i := range frame.Cb {
		frame.Cb[i], frame.Cr[i] = 128, 128
	}
	if err := frame.setDisplayCrop(2, 2, 2, 2); err != nil {
		t.Fatal(err)
	}
	canvas, _ := conb.NewCanvas(2, 2)
	if err := frame.DrawCanvas(canvas); err != nil {
		t.Fatal(err)
	}
	first, _ := canvas.Pixel(0, 0)
	if first.R < 11 || first.R > 12 || first.G != first.R || first.B != first.R {
		t.Fatalf("cropped first pixel = %+v", first)
	}
}

func TestFrameMacroblockRoundTrip(t *testing.T) {
	frame, _ := NewFrame420(16, 16)
	var luma [256]uint8
	for i := range luma {
		luma[i] = uint8(i)
	}
	if err := frame.WriteLumaMacroblock(0, 0, luma); err != nil {
		t.Fatal(err)
	}
	for i, value := range frame.Y {
		if value != luma[i] {
			t.Fatalf("Y[%d] = %d, want %d", i, value, luma[i])
		}
	}
}
