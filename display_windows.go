//go:build windows

package conb

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const (
	csHRedraw = 0x0002
	csVRedraw = 0x0001

	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	cwUseDefault       = ^uintptr(0x7fffffff)
	swShow             = 5

	wmDestroy = 0x0002
	wmPaint   = 0x000F
	wmClose   = 0x0010
	wmQuit    = 0x0012

	pmRemove = 0x0001

	idcArrow    = 32512
	colorWindow = 5

	biRGB        = 0
	dibRGBColors = 0
	srccopy      = 0x00CC0020
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procUpdateWindow     = user32.NewProc("UpdateWindow")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procAdjustWindowRect = user32.NewProc("AdjustWindowRect")
	procPeekMessageW     = user32.NewProc("PeekMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procBeginPaint       = user32.NewProc("BeginPaint")
	procEndPaint         = user32.NewProc("EndPaint")
	procGetClientRect    = user32.NewProc("GetClientRect")
	procInvalidateRect   = user32.NewProc("InvalidateRect")
	procLoadCursorW      = user32.NewProc("LoadCursorW")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procStretchDIBits    = gdi32.NewProc("StretchDIBits")

	windowClassName = syscall.StringToUTF16Ptr("ConbNativeDisplay")
	windowProcPtr   = syscall.NewCallback(windowProc)
	registerOnce    sync.Once
	registerErr     error
	windowsMu       sync.RWMutex
	windows         = make(map[uintptr]*windowsDisplay)
)

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

type message struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type paintStruct struct {
	HDC         uintptr
	Erase       int32
	Paint       rect
	Restore     int32
	IncUpdate   int32
	RGBReserved [32]byte
}

type windowClassEx struct {
	Size, Style             uint32
	WndProc                 uintptr
	ClassExtra, WindowExtra int32
	Instance, Icon, Cursor  uintptr
	Background              uintptr
	MenuName, ClassName     *uint16
	IconSmall               uintptr
}

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type windowsDisplay struct {
	hwnd          uintptr
	width, height int
	pixelsMu      sync.RWMutex
	pixels        []byte
	shouldClose   atomic.Bool
	closed        atomic.Bool
}

var _ Display = (*windowsDisplay)(nil)

// NewDisplay creates a Win32 window with the requested client-area size.
// The caller must call PollEvents and Close from the goroutine that called
// NewDisplay because a Win32 message queue belongs to an OS thread.
func NewDisplay(width, height int, title string) (Display, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("display dimensions must be positive: %dx%d", width, height)
	}
	const maxInt32 = int(^uint32(0) >> 1)
	maxInt := int(^uint(0) >> 1)
	if width > maxInt32 || height > maxInt32 || width > maxInt/height/4 {
		return nil, fmt.Errorf("display dimensions are too large: %dx%d", width, height)
	}

	runtime.LockOSThread()
	succeeded := false
	defer func() {
		if !succeeded {
			runtime.UnlockOSThread()
		}
	}()

	registerOnce.Do(registerWindowClass)
	if registerErr != nil {
		return nil, registerErr
	}

	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return nil, fmt.Errorf("invalid window title: %w", err)
	}

	windowRect := rect{Right: int32(width), Bottom: int32(height)}
	if result, _, callErr := procAdjustWindowRect.Call(
		uintptr(unsafe.Pointer(&windowRect)), wsOverlappedWindow, 0,
	); result == 0 {
		return nil, win32Error("AdjustWindowRect", callErr)
	}

	instance, _, callErr := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return nil, win32Error("GetModuleHandleW", callErr)
	}

	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(windowClassName)),
		uintptr(unsafe.Pointer(titlePtr)),
		wsOverlappedWindow|wsVisible,
		cwUseDefault, cwUseDefault,
		uintptr(windowRect.Right-windowRect.Left),
		uintptr(windowRect.Bottom-windowRect.Top),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return nil, win32Error("CreateWindowExW", callErr)
	}

	display := &windowsDisplay{
		hwnd:   hwnd,
		width:  width,
		height: height,
		pixels: make([]byte, width*height*4),
	}
	windowsMu.Lock()
	windows[hwnd] = display
	windowsMu.Unlock()

	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
	succeeded = true
	return display, nil
}

func registerWindowClass() {
	instance, _, callErr := procGetModuleHandleW.Call(0)
	if instance == 0 {
		registerErr = win32Error("GetModuleHandleW", callErr)
		return
	}
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	class := windowClassEx{
		Size:       uint32(unsafe.Sizeof(windowClassEx{})),
		Style:      csHRedraw | csVRedraw,
		WndProc:    windowProcPtr,
		Instance:   instance,
		Cursor:     cursor,
		Background: colorWindow + 1,
		ClassName:  windowClassName,
	}
	if atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		registerErr = win32Error("RegisterClassExW", callErr)
	}
}

func (d *windowsDisplay) Width() int  { return d.width }
func (d *windowsDisplay) Height() int { return d.height }

func (d *windowsDisplay) Present(canvas *Canvas) error {
	if canvas == nil {
		return fmt.Errorf("cannot present a nil canvas")
	}
	if canvas.Width() != d.width || canvas.Height() != d.height {
		return fmt.Errorf("canvas size %dx%d does not match display size %dx%d",
			canvas.Width(), canvas.Height(), d.width, d.height)
	}
	if d.closed.Load() {
		return fmt.Errorf("display is closed")
	}

	source := canvas.Pixels()
	d.pixelsMu.Lock()
	for i := 0; i < len(source); i += 4 {
		// A 32-bit BI_RGB DIB uses B, G, R, unused byte order.
		d.pixels[i] = source[i+2]
		d.pixels[i+1] = source[i+1]
		d.pixels[i+2] = source[i]
		d.pixels[i+3] = source[i+3]
	}
	d.pixelsMu.Unlock()

	procInvalidateRect.Call(d.hwnd, 0, 0)
	return nil
}

func (d *windowsDisplay) PollEvents() error {
	if d.closed.Load() {
		return nil
	}
	var msg message
	for {
		hasMessage, _, _ := procPeekMessageW.Call(
			uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove,
		)
		if hasMessage == 0 {
			return nil
		}
		if msg.Message == wmQuit {
			d.shouldClose.Store(true)
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (d *windowsDisplay) ShouldClose() bool { return d.shouldClose.Load() }

func (d *windowsDisplay) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	d.shouldClose.Store(true)
	windowsMu.Lock()
	delete(windows, d.hwnd)
	windowsMu.Unlock()
	result, _, callErr := procDestroyWindow.Call(d.hwnd)
	runtime.UnlockOSThread()
	if result == 0 {
		return win32Error("DestroyWindow", callErr)
	}
	return nil
}

func windowProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	windowsMu.RLock()
	display := windows[hwnd]
	windowsMu.RUnlock()

	if display != nil {
		switch msg {
		case wmClose:
			display.shouldClose.Store(true)
			return 0
		case wmDestroy:
			display.shouldClose.Store(true)
			return 0
		case wmPaint:
			display.paint()
			return 0
		}
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return result
}

func (d *windowsDisplay) paint() {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(d.hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(d.hwnd, uintptr(unsafe.Pointer(&ps)))

	var client rect
	procGetClientRect.Call(d.hwnd, uintptr(unsafe.Pointer(&client)))
	info := bitmapInfo{Header: bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(d.width),
		Height:      -int32(d.height), // negative means top-down rows
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}}

	d.pixelsMu.RLock()
	defer d.pixelsMu.RUnlock()
	if len(d.pixels) == 0 {
		return
	}
	procStretchDIBits.Call(
		hdc,
		0, 0, uintptr(client.Right), uintptr(client.Bottom),
		0, 0, uintptr(d.width), uintptr(d.height),
		uintptr(unsafe.Pointer(&d.pixels[0])),
		uintptr(unsafe.Pointer(&info)),
		dibRGBColors, srccopy,
	)
}

func win32Error(operation string, err error) error {
	if errno, ok := err.(syscall.Errno); ok && errno == 0 {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s failed: %w", operation, err)
}
