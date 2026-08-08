//go:build !windows && !linux

package conb

import (
	"errors"
	"runtime"
)

// ErrDisplayUnsupported indicates that a native display implementation is not
// available for the current operating system yet.
var ErrDisplayUnsupported = errors.New("native display is not supported on " + runtime.GOOS)

// NewDisplay creates the native display for the current operating system.
func NewDisplay(width, height int, title string) (Display, error) {
	return nil, ErrDisplayUnsupported
}
