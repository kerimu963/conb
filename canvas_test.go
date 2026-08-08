package conb

import "testing"

func TestNewCanvas(t *testing.T) {
	c, err := NewCanvas(3, 2)
	if err != nil {
		t.Fatalf("NewCanvas returned an error: %v", err)
	}
	if c.Width() != 3 || c.Height() != 2 {
		t.Fatalf("size = %dx%d, want 3x2", c.Width(), c.Height())
	}
	if len(c.Pixels()) != 3*2*4 {
		t.Fatalf("buffer size = %d, want 24", len(c.Pixels()))
	}
}

func TestNewCanvasRejectsInvalidSize(t *testing.T) {
	for _, size := range [][2]int{{0, 1}, {1, 0}, {-1, 1}, {1, -1}} {
		if _, err := NewCanvas(size[0], size[1]); err == nil {
			t.Errorf("NewCanvas(%d, %d) returned no error", size[0], size[1])
		}
	}
}

func TestSetAndGetPixel(t *testing.T) {
	c, _ := NewCanvas(2, 2)
	want := Color{R: 12, G: 34, B: 56, A: 255}
	if !c.SetPixel(1, 0, want) {
		t.Fatal("SetPixel rejected an in-bounds coordinate")
	}
	got, ok := c.Pixel(1, 0)
	if !ok || got != want {
		t.Fatalf("Pixel(1, 0) = (%+v, %v), want (%+v, true)", got, ok, want)
	}
}

func TestOutOfBoundsPixel(t *testing.T) {
	c, _ := NewCanvas(2, 2)
	if c.SetPixel(-1, 0, Color{A: 255}) {
		t.Fatal("SetPixel accepted an out-of-bounds coordinate")
	}
	if _, ok := c.Pixel(2, 0); ok {
		t.Fatal("Pixel accepted an out-of-bounds coordinate")
	}
}

func TestClear(t *testing.T) {
	c, _ := NewCanvas(2, 3)
	want := Color{R: 1, G: 2, B: 3, A: 4}
	c.Clear(want)

	for y := 0; y < c.Height(); y++ {
		for x := 0; x < c.Width(); x++ {
			got, _ := c.Pixel(x, y)
			if got != want {
				t.Fatalf("Pixel(%d, %d) = %+v, want %+v", x, y, got, want)
			}
		}
	}
}
