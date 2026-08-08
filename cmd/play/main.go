package main

import (
	"fmt"
	"os"
	"time"

	"conb"
	"conb/convert/h264"
	"conb/video"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "conb-play:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: go run ./cmd/play <video.mp4>")
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stream, err := video.OpenMP4(file, info.Size())
	if err != nil {
		return err
	}
	order := stream.PresentationOrder()
	if len(order) == 0 {
		return fmt.Errorf("video has no frames")
	}
	decoded := make(map[int]*h264.Frame420)
	nextDecode := 0
	decodeThrough := func(index int) error {
		for nextDecode <= index {
			frame, decodeErr := stream.Decode(nextDecode)
			if decodeErr != nil {
				return decodeErr
			}
			decoded[nextDecode] = frame
			nextDecode++
		}
		return nil
	}
	if err = decodeThrough(order[0]); err != nil {
		return err
	}
	first := decoded[order[0]]
	displayWidth, displayHeight := first.DisplaySize()
	canvas, err := conb.NewCanvas(displayWidth, displayHeight)
	if err != nil {
		return err
	}
	display, err := conb.NewDisplay(displayWidth, displayHeight, "conb MP4 player")
	if err != nil {
		return err
	}
	defer display.Close()

	for position, sampleIndex := range order {
		if display.ShouldClose() {
			break
		}
		start := time.Now()
		if err = decodeThrough(sampleIndex); err != nil {
			return err
		}
		frame := decoded[sampleIndex]
		delete(decoded, sampleIndex)
		frameWidth, frameHeight := frame.DisplaySize()
		if frameWidth != canvas.Width() || frameHeight != canvas.Height() {
			return fmt.Errorf("sample %d changes display size to %dx%d", sampleIndex, frameWidth, frameHeight)
		}
		if err = frame.DrawCanvas(canvas); err != nil {
			return err
		}
		if err = display.Present(canvas); err != nil {
			return err
		}
		duration, err := stream.PresentationDuration(order, position)
		if err != nil {
			return err
		}
		for time.Since(start) < duration && !display.ShouldClose() {
			if err = display.PollEvents(); err != nil {
				return err
			}
			remaining := duration - time.Since(start)
			if remaining > 4*time.Millisecond {
				remaining = 4 * time.Millisecond
			}
			if remaining > 0 {
				time.Sleep(remaining)
			}
		}
	}
	return nil
}
