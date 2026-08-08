package main

import (
	"fmt"
	"os"
	"time"

	"conb"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	const width, height = 640, 480
	canvas, err := conb.NewCanvas(width, height)
	if err != nil {
		return err
	}

	// Draw a simple gradient directly into our own pixel buffer.
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.SetPixel(x, y, conb.Color{
				R: uint8(x * 255 / (width - 1)),
				G: uint8(y * 255 / (height - 1)),
				B: 96,
				A: 255,
			})
		}
	}

	display, err := conb.NewDisplay(width, height, "conb canvas")
	if err != nil {
		return err
	}
	defer display.Close()

	if err := display.Present(canvas); err != nil {
		return err
	}
	for !display.ShouldClose() {
		if err := display.PollEvents(); err != nil {
			return err
		}
		time.Sleep(time.Millisecond * 8)
	}
	return nil
}
