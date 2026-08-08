package conb

import "fmt"

// Color represents one pixel in 8-bit RGBA format.
type Color struct {
	R, G, B, A uint8
}

// Canvas is a two-dimensional RGBA pixel buffer.
// Pixels are stored from left to right, top to bottom.
type Canvas struct {
	width  int
	height int
	pixels []byte
}

// NewCanvas creates a transparent canvas of the requested size.
func NewCanvas(width, height int) (*Canvas, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("canvas dimensions must be positive: %dx%d", width, height)
	}

	// Check the multiplication before allocating so an overflowing size cannot
	// become a misleadingly small buffer.
	const bytesPerPixel = 4
	maxInt := int(^uint(0) >> 1)
	if width > maxInt/height/bytesPerPixel {
		return nil, fmt.Errorf("canvas dimensions are too large: %dx%d", width, height)
	}

	return &Canvas{
		width:  width,
		height: height,
		pixels: make([]byte, width*height*bytesPerPixel),
	}, nil
}

func (c *Canvas) Width() int  { return c.width }
func (c *Canvas) Height() int { return c.height }

// Pixels returns the underlying RGBA buffer. Modifying it changes the canvas.
func (c *Canvas) Pixels() []byte { return c.pixels }

// SetPixel sets a pixel and reports whether the coordinate was in bounds.
func (c *Canvas) SetPixel(x, y int, color Color) bool {
	if !c.inBounds(x, y) {
		return false
	}

	i := (y*c.width + x) * 4
	c.pixels[i] = color.R
	c.pixels[i+1] = color.G
	c.pixels[i+2] = color.B
	c.pixels[i+3] = color.A
	return true
}

// Pixel returns a pixel and reports whether the coordinate was in bounds.
func (c *Canvas) Pixel(x, y int) (Color, bool) {
	if !c.inBounds(x, y) {
		return Color{}, false
	}

	i := (y*c.width + x) * 4
	return Color{
		R: c.pixels[i],
		G: c.pixels[i+1],
		B: c.pixels[i+2],
		A: c.pixels[i+3],
	}, true
}

// Clear fills every pixel with the supplied color.
func (c *Canvas) Clear(color Color) {
	for i := 0; i < len(c.pixels); i += 4 {
		c.pixels[i] = color.R
		c.pixels[i+1] = color.G
		c.pixels[i+2] = color.B
		c.pixels[i+3] = color.A
	}
}

func (c *Canvas) inBounds(x, y int) bool {
	return x >= 0 && x < c.width && y >= 0 && y < c.height
}
