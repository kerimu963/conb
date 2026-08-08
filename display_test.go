package conb

import "testing"

// This compile-time check documents that platform types do not inherit
// Display: implementing all its methods is sufficient.
var _ Display = (*testDisplay)(nil)

type testDisplay struct {
	width, height int
	closed        bool
}

func (d *testDisplay) Width() int            { return d.width }
func (d *testDisplay) Height() int           { return d.height }
func (d *testDisplay) Present(*Canvas) error { return nil }
func (d *testDisplay) PollEvents() error     { return nil }
func (d *testDisplay) ShouldClose() bool     { return d.closed }
func (d *testDisplay) Close() error          { d.closed = true; return nil }

func TestDisplayCanBeImplementedWithoutPlatformDependencies(t *testing.T) {
	display := Display(&testDisplay{width: 640, height: 480})
	if display.Width() != 640 || display.Height() != 480 {
		t.Fatalf("display size = %dx%d, want 640x480", display.Width(), display.Height())
	}

	if err := display.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if !display.ShouldClose() {
		t.Fatal("display remains open after Close")
	}
}
