package conb

// Display is the operating-system-independent boundary for showing a Canvas.
//
// A platform implementation owns the native window and copies the canvas to
// it in Present. PollEvents must be called regularly so the native window can
// process input, resizing, and close requests.
type Display interface {
	Width() int
	Height() int
	Present(canvas *Canvas) error
	PollEvents() error
	ShouldClose() bool
	Close() error
}
