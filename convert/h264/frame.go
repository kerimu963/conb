package h264

import (
	"fmt"

	"conb"
)

// Frame420 stores one progressive 8-bit YUV 4:2:0 decoded picture.
type Frame420 struct {
	Width, Height int
	Y, Cb, Cr     []uint8
	displayX      int
	displayY      int
	displayWidth  int
	displayHeight int
	poc           int64
	longTerm      bool
	motion        [2]map[[2]int]motionInfo
}

// DisplaySize reports the cropped dimensions intended for presentation.
func (f *Frame420) DisplaySize() (width, height int) {
	if f == nil {
		return 0, 0
	}
	if f.displayWidth == 0 || f.displayHeight == 0 {
		return f.Width, f.Height
	}
	return f.displayWidth, f.displayHeight
}

func (f *Frame420) setDisplayCrop(x, y, width, height int) error {
	if x < 0 || y < 0 || width <= 0 || height <= 0 || x+width > f.Width || y+height > f.Height {
		return fmt.Errorf("invalid frame crop (%d,%d) %dx%d", x, y, width, height)
	}
	f.displayX, f.displayY = x, y
	f.displayWidth, f.displayHeight = width, height
	return nil
}

func NewFrame420(width, height int) (*Frame420, error) {
	if width <= 0 || height <= 0 || width%2 != 0 || height%2 != 0 || width > int(^uint(0)>>1)/height {
		return nil, fmt.Errorf("invalid YUV420 frame size %dx%d", width, height)
	}
	luma := width * height
	return &Frame420{Width: width, Height: height, Y: make([]uint8, luma), Cb: make([]uint8, luma/4), Cr: make([]uint8, luma/4)}, nil
}

func (f *Frame420) WriteLumaMacroblock(macroblockX, macroblockY int, samples [256]uint8) error {
	x0, y0 := macroblockX*16, macroblockY*16
	if f == nil || x0 < 0 || y0 < 0 || x0+16 > f.Width || y0+16 > f.Height {
		return fmt.Errorf("luma macroblock (%d,%d) is outside frame", macroblockX, macroblockY)
	}
	for y := range 16 {
		copy(f.Y[(y0+y)*f.Width+x0:(y0+y)*f.Width+x0+16], samples[y*16:(y+1)*16])
	}
	return nil
}

func (f *Frame420) WriteChromaMacroblock(macroblockX, macroblockY int, cb, cr [64]uint8) error {
	if f == nil {
		return fmt.Errorf("nil frame")
	}
	width := f.Width / 2
	x0, y0 := macroblockX*8, macroblockY*8
	if x0 < 0 || y0 < 0 || x0+8 > width || y0+8 > f.Height/2 {
		return fmt.Errorf("chroma macroblock (%d,%d) is outside frame", macroblockX, macroblockY)
	}
	for y := range 8 {
		start := (y0+y)*width + x0
		copy(f.Cb[start:start+8], cb[y*8:(y+1)*8])
		copy(f.Cr[start:start+8], cr[y*8:(y+1)*8])
	}
	return nil
}

func (f *Frame420) Intra16Neighbours(macroblockX, macroblockY int) Intra16Neighbours {
	x0, y0 := macroblockX*16, macroblockY*16
	var n Intra16Neighbours
	if y0 > 0 {
		n.TopAvailable = true
		copy(n.Top[:], f.Y[(y0-1)*f.Width+x0:(y0-1)*f.Width+x0+16])
	}
	if x0 > 0 {
		n.LeftAvailable = true
		for y := range 16 {
			n.Left[y] = f.Y[(y0+y)*f.Width+x0-1]
		}
	}
	if x0 > 0 && y0 > 0 {
		n.TopLeftAvailable = true
		n.TopLeft = f.Y[(y0-1)*f.Width+x0-1]
	}
	return n
}

func (f *Frame420) ChromaNeighbours(macroblockX, macroblockY, component int) ChromaNeighbours420 {
	plane := f.Cb
	if component == 1 {
		plane = f.Cr
	}
	width := f.Width / 2
	x0, y0 := macroblockX*8, macroblockY*8
	var n ChromaNeighbours420
	if y0 > 0 {
		n.TopAvailable = true
		copy(n.Top[:], plane[(y0-1)*width+x0:(y0-1)*width+x0+8])
	}
	if x0 > 0 {
		n.LeftAvailable = true
		for y := range 8 {
			n.Left[y] = plane[(y0+y)*width+x0-1]
		}
	}
	if x0 > 0 && y0 > 0 {
		n.TopLeftAvailable = true
		n.TopLeft = plane[(y0-1)*width+x0-1]
	}
	return n
}

// DrawCanvas converts limited-range BT.601 YUV into the Canvas RGBA buffer.
func (f *Frame420) DrawCanvas(canvas *conb.Canvas) error {
	displayWidth, displayHeight := f.DisplaySize()
	if f == nil || canvas == nil || canvas.Width() != displayWidth || canvas.Height() != displayHeight {
		return fmt.Errorf("frame and canvas sizes do not match")
	}
	pixels := canvas.Pixels()
	for y := 0; y < displayHeight; y++ {
		for x := 0; x < displayWidth; x++ {
			sourceX, sourceY := x+f.displayX, y+f.displayY
			yValue := int(f.Y[sourceY*f.Width+sourceX]) - 16
			if yValue < 0 {
				yValue = 0
			}
			chromaIndex := (sourceY/2)*(f.Width/2) + sourceX/2
			cb, cr := int(f.Cb[chromaIndex])-128, int(f.Cr[chromaIndex])-128
			position := (y*displayWidth + x) * 4
			pixels[position] = clipByte(int64((298*yValue + 409*cr + 128) >> 8))
			pixels[position+1] = clipByte(int64((298*yValue - 100*cb - 208*cr + 128) >> 8))
			pixels[position+2] = clipByte(int64((298*yValue + 516*cb + 128) >> 8))
			pixels[position+3] = 255
		}
	}
	return nil
}
