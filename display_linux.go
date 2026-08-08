//go:build linux

package conb

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	x11Expose          = 12
	x11DestroyNotify   = 17
	x11ClientMessage   = 33
	x11EventExposure   = 1 << 15
	x11EventStructure  = 1 << 17
	x11ImageZPixmap    = 2
	x11PropertyReplace = 0
	x11AtomAtom        = 4
	x11AtomString      = 31
	x11AtomWMName      = 39
)

type x11Display struct {
	conn                    net.Conn
	reader                  *bufio.Reader
	writeMu                 sync.Mutex
	window, gc              uint32
	width, height           int
	depth, bitsPerPixel     uint8
	scanlinePad             uint8
	imageByteOrder          uint8
	redMask, greenMask      uint32
	blueMask                uint32
	maxRequestBytes         int
	wmProtocols, wmDelete   uint32
	shouldClose, closed     bool
	lastCanvas              *Canvas
	resourceBase            uint32
	resourceMask, resourceN uint32
}

var _ Display = (*x11Display)(nil)

type x11Setup struct {
	resourceBase, resourceMask uint32
	root, blackPixel           uint32
	rootVisual                 uint32
	rootDepth                  uint8
	bitsPerPixel, scanlinePad  uint8
	imageByteOrder             uint8
	redMask, greenMask         uint32
	blueMask                   uint32
	maxRequestBytes            int
}

// NewDisplay connects directly to the X11 server named by DISPLAY.
func NewDisplay(width, height int, title string) (Display, error) {
	if width <= 0 || height <= 0 || width > 65535 || height > 65535 {
		return nil, fmt.Errorf("invalid X11 display dimensions: %dx%d", width, height)
	}

	conn, displayNumber, err := dialX11(os.Getenv("DISPLAY"))
	if err != nil {
		return nil, err
	}
	fail := func(err error) (Display, error) {
		conn.Close()
		return nil, err
	}

	authName, authData := x11Authority(displayNumber)
	setup, err := initializeX11(conn, authName, authData)
	if err != nil {
		return fail(err)
	}

	d := &x11Display{
		conn:            conn,
		reader:          bufio.NewReader(conn),
		width:           width,
		height:          height,
		depth:           setup.rootDepth,
		bitsPerPixel:    setup.bitsPerPixel,
		scanlinePad:     setup.scanlinePad,
		imageByteOrder:  setup.imageByteOrder,
		redMask:         setup.redMask,
		greenMask:       setup.greenMask,
		blueMask:        setup.blueMask,
		maxRequestBytes: setup.maxRequestBytes,
		resourceBase:    setup.resourceBase,
		resourceMask:    setup.resourceMask,
	}

	d.wmProtocols, err = d.internAtom("WM_PROTOCOLS")
	if err != nil {
		return fail(err)
	}
	d.wmDelete, err = d.internAtom("WM_DELETE_WINDOW")
	if err != nil {
		return fail(err)
	}
	d.window = d.nextResourceID()
	d.gc = d.nextResourceID()

	create := make([]byte, 40)
	create[0] = 1 // CreateWindow
	create[1] = 0 // CopyFromParent depth
	binary.LittleEndian.PutUint16(create[2:], 10)
	binary.LittleEndian.PutUint32(create[4:], d.window)
	binary.LittleEndian.PutUint32(create[8:], setup.root)
	binary.LittleEndian.PutUint16(create[16:], uint16(width))
	binary.LittleEndian.PutUint16(create[18:], uint16(height))
	binary.LittleEndian.PutUint16(create[22:], 1)              // InputOutput
	binary.LittleEndian.PutUint32(create[28:], (1<<1)|(1<<11)) // background + event mask
	binary.LittleEndian.PutUint32(create[32:], setup.blackPixel)
	binary.LittleEndian.PutUint32(create[36:], x11EventExposure|x11EventStructure)
	if err := d.write(create); err != nil {
		return fail(fmt.Errorf("create X11 window: %w", err))
	}

	createGC := make([]byte, 16)
	createGC[0] = 55
	binary.LittleEndian.PutUint16(createGC[2:], 4)
	binary.LittleEndian.PutUint32(createGC[4:], d.gc)
	binary.LittleEndian.PutUint32(createGC[8:], d.window)
	if err := d.write(createGC); err != nil {
		return fail(fmt.Errorf("create X11 graphics context: %w", err))
	}
	if err := d.changeProperty(d.wmProtocols, x11AtomAtom, 32, u32Bytes(d.wmDelete)); err != nil {
		return fail(err)
	}
	if title != "" {
		if err := d.changeProperty(x11AtomWMName, x11AtomString, 8, []byte(title)); err != nil {
			return fail(err)
		}
	}
	mapWindow := make([]byte, 8)
	mapWindow[0] = 8
	binary.LittleEndian.PutUint16(mapWindow[2:], 2)
	binary.LittleEndian.PutUint32(mapWindow[4:], d.window)
	if err := d.write(mapWindow); err != nil {
		return fail(fmt.Errorf("map X11 window: %w", err))
	}
	return d, nil
}

func (d *x11Display) Width() int  { return d.width }
func (d *x11Display) Height() int { return d.height }

func (d *x11Display) Present(canvas *Canvas) error {
	if canvas == nil {
		return errors.New("cannot present a nil canvas")
	}
	if canvas.Width() != d.width || canvas.Height() != d.height {
		return fmt.Errorf("canvas size %dx%d does not match display size %dx%d", canvas.Width(), canvas.Height(), d.width, d.height)
	}
	if d.closed {
		return errors.New("display is closed")
	}
	d.lastCanvas = canvas
	return d.putImage(canvas)
}

func (d *x11Display) PollEvents() error {
	if d.closed {
		return nil
	}
	for {
		if err := d.conn.SetReadDeadline(time.Now().Add(time.Millisecond)); err != nil {
			return err
		}
		event := make([]byte, 32)
		_, err := io.ReadFull(d.reader, event)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				d.conn.SetReadDeadline(time.Time{})
				return nil
			}
			d.shouldClose = true
			return fmt.Errorf("read X11 event: %w", err)
		}
		switch event[0] & 0x7f {
		case 0:
			return fmt.Errorf("X11 protocol error %d in request %d", event[1], event[10])
		case x11Expose:
			if d.lastCanvas != nil && binary.LittleEndian.Uint16(event[16:]) == 0 {
				if err := d.putImage(d.lastCanvas); err != nil {
					return err
				}
			}
		case x11DestroyNotify:
			d.shouldClose = true
		case x11ClientMessage:
			if binary.LittleEndian.Uint32(event[8:]) == d.wmProtocols &&
				binary.LittleEndian.Uint32(event[12:]) == d.wmDelete {
				d.shouldClose = true
			}
		}
	}
}

func (d *x11Display) ShouldClose() bool { return d.shouldClose }

func (d *x11Display) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	d.shouldClose = true
	req := make([]byte, 8)
	req[0] = 4 // DestroyWindow
	binary.LittleEndian.PutUint16(req[2:], 2)
	binary.LittleEndian.PutUint32(req[4:], d.window)
	writeErr := d.write(req)
	closeErr := d.conn.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (d *x11Display) putImage(canvas *Canvas) error {
	rowBytes := aligned(d.width*int(d.bitsPerPixel), int(d.scanlinePad)) / 8
	maxData := d.maxRequestBytes - 24
	rows := maxData / rowBytes
	if rows < 1 {
		return fmt.Errorf("X11 maximum request is too small for a %d-byte scanline", rowBytes)
	}
	if rows > d.height {
		rows = d.height
	}

	for y := 0; y < d.height; y += rows {
		h := rows
		if y+h > d.height {
			h = d.height - y
		}
		data := make([]byte, rowBytes*h)
		d.encodePixels(data, canvas.Pixels(), y, h, rowBytes)
		padded := aligned(len(data), 4)
		req := make([]byte, 24+padded)
		req[0], req[1] = 72, x11ImageZPixmap
		binary.LittleEndian.PutUint16(req[2:], uint16(len(req)/4))
		binary.LittleEndian.PutUint32(req[4:], d.window)
		binary.LittleEndian.PutUint32(req[8:], d.gc)
		binary.LittleEndian.PutUint16(req[12:], uint16(d.width))
		binary.LittleEndian.PutUint16(req[14:], uint16(h))
		binary.LittleEndian.PutUint16(req[18:], uint16(y))
		req[21] = d.depth
		copy(req[24:], data)
		if err := d.write(req); err != nil {
			return fmt.Errorf("send pixels to X11: %w", err)
		}
	}
	return nil
}

func (d *x11Display) encodePixels(dst, src []byte, firstY, rows, stride int) {
	bytesPerPixel := int(d.bitsPerPixel) / 8
	for row := 0; row < rows; row++ {
		for x := 0; x < d.width; x++ {
			si := ((firstY+row)*d.width + x) * 4
			pixel := scaleToMask(src[si], d.redMask) |
				scaleToMask(src[si+1], d.greenMask) |
				scaleToMask(src[si+2], d.blueMask)
			di := row*stride + x*bytesPerPixel
			if d.imageByteOrder == 0 {
				for b := 0; b < bytesPerPixel; b++ {
					dst[di+b] = byte(pixel >> (8 * b))
				}
			} else {
				for b := 0; b < bytesPerPixel; b++ {
					dst[di+b] = byte(pixel >> (8 * (bytesPerPixel - 1 - b)))
				}
			}
		}
	}
}

func scaleToMask(value byte, mask uint32) uint32 {
	if mask == 0 {
		return 0
	}
	shift := 0
	for (mask>>shift)&1 == 0 {
		shift++
	}
	max := mask >> shift
	return (uint32(value) * max / 255) << shift & mask
}

func (d *x11Display) internAtom(name string) (uint32, error) {
	req := make([]byte, 8+aligned(len(name), 4))
	req[0] = 16
	binary.LittleEndian.PutUint16(req[2:], uint16(len(req)/4))
	binary.LittleEndian.PutUint16(req[4:], uint16(len(name)))
	copy(req[8:], name)
	if err := d.write(req); err != nil {
		return 0, err
	}
	reply := make([]byte, 32)
	if _, err := io.ReadFull(d.reader, reply); err != nil {
		return 0, fmt.Errorf("read X11 InternAtom reply: %w", err)
	}
	if reply[0] == 0 {
		return 0, fmt.Errorf("X11 InternAtom error %d", reply[1])
	}
	return binary.LittleEndian.Uint32(reply[8:]), nil
}

func (d *x11Display) changeProperty(property, propertyType uint32, format byte, data []byte) error {
	padded := aligned(len(data), 4)
	req := make([]byte, 24+padded)
	req[0], req[1] = 18, x11PropertyReplace
	binary.LittleEndian.PutUint16(req[2:], uint16(len(req)/4))
	binary.LittleEndian.PutUint32(req[4:], d.window)
	binary.LittleEndian.PutUint32(req[8:], property)
	binary.LittleEndian.PutUint32(req[12:], propertyType)
	req[16] = format
	items := len(data)
	if format != 0 {
		items = len(data) * 8 / int(format)
	}
	binary.LittleEndian.PutUint32(req[20:], uint32(items))
	copy(req[24:], data)
	return d.write(req)
}

func (d *x11Display) nextResourceID() uint32 {
	d.resourceN++
	return d.resourceBase | (d.resourceN & d.resourceMask)
}

func (d *x11Display) write(data []byte) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	_, err := d.conn.Write(data)
	return err
}

func dialX11(display string) (net.Conn, string, error) {
	if display == "" {
		return nil, "", errors.New("DISPLAY is not set; an X11 server is required")
	}
	host, rest, ok := strings.Cut(display, ":")
	if !ok {
		return nil, "", fmt.Errorf("invalid DISPLAY value %q", display)
	}
	number := strings.SplitN(rest, ".", 2)[0]
	n, err := strconv.Atoi(number)
	if err != nil || n < 0 {
		return nil, "", fmt.Errorf("invalid DISPLAY value %q", display)
	}
	var conn net.Conn
	if host == "" || host == "unix" {
		conn, err = net.Dial("unix", "/tmp/.X11-unix/X"+number)
	} else {
		conn, err = net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(6000+n)))
	}
	if err != nil {
		return nil, "", fmt.Errorf("connect to X11 display %q: %w", display, err)
	}
	return conn, number, nil
}

func initializeX11(conn net.Conn, authName, authData []byte) (x11Setup, error) {
	req := make([]byte, 12+aligned(len(authName), 4)+aligned(len(authData), 4))
	req[0] = 'l'
	binary.LittleEndian.PutUint16(req[2:], 11)
	binary.LittleEndian.PutUint16(req[6:], uint16(len(authName)))
	binary.LittleEndian.PutUint16(req[8:], uint16(len(authData)))
	copy(req[12:], authName)
	copy(req[12+aligned(len(authName), 4):], authData)
	if _, err := conn.Write(req); err != nil {
		return x11Setup{}, err
	}
	header := make([]byte, 8)
	if _, err := io.ReadFull(conn, header); err != nil {
		return x11Setup{}, fmt.Errorf("read X11 setup: %w", err)
	}
	extra := make([]byte, int(binary.LittleEndian.Uint16(header[6:]))*4)
	if _, err := io.ReadFull(conn, extra); err != nil {
		return x11Setup{}, fmt.Errorf("read X11 setup body: %w", err)
	}
	if header[0] != 1 {
		reasonLen := int(header[1])
		if reasonLen > len(extra) {
			reasonLen = len(extra)
		}
		return x11Setup{}, fmt.Errorf("X11 connection rejected: %s", string(extra[:reasonLen]))
	}
	return parseX11Setup(extra)
}

func parseX11Setup(data []byte) (x11Setup, error) {
	if len(data) < 32 {
		return x11Setup{}, errors.New("X11 setup response is truncated")
	}
	s := x11Setup{
		resourceBase:    binary.LittleEndian.Uint32(data[4:]),
		resourceMask:    binary.LittleEndian.Uint32(data[8:]),
		maxRequestBytes: int(binary.LittleEndian.Uint16(data[18:])) * 4,
		imageByteOrder:  data[22],
	}
	vendorLen := int(binary.LittleEndian.Uint16(data[16:]))
	formatCount, screenCount := int(data[21]), int(data[20])
	offset := 32 + aligned(vendorLen, 4)
	type pixmapFormat struct{ depth, bpp, pad uint8 }
	formats := make([]pixmapFormat, 0, formatCount)
	for i := 0; i < formatCount; i++ {
		if offset+8 > len(data) {
			return x11Setup{}, errors.New("X11 pixmap formats are truncated")
		}
		formats = append(formats, pixmapFormat{data[offset], data[offset+1], data[offset+2]})
		offset += 8
	}
	if screenCount == 0 || offset+40 > len(data) {
		return x11Setup{}, errors.New("X11 setup has no screen")
	}
	s.root = binary.LittleEndian.Uint32(data[offset:])
	s.blackPixel = binary.LittleEndian.Uint32(data[offset+12:])
	s.rootVisual = binary.LittleEndian.Uint32(data[offset+32:])
	s.rootDepth = data[offset+38]
	depthCount := int(data[offset+39])
	offset += 40
	for _, format := range formats {
		if format.depth == s.rootDepth {
			s.bitsPerPixel, s.scanlinePad = format.bpp, format.pad
			break
		}
	}
	for i := 0; i < depthCount; i++ {
		if offset+8 > len(data) {
			break
		}
		visualCount := int(binary.LittleEndian.Uint16(data[offset+2:]))
		offset += 8
		for j := 0; j < visualCount && offset+24 <= len(data); j++ {
			if binary.LittleEndian.Uint32(data[offset:]) == s.rootVisual {
				s.redMask = binary.LittleEndian.Uint32(data[offset+8:])
				s.greenMask = binary.LittleEndian.Uint32(data[offset+12:])
				s.blueMask = binary.LittleEndian.Uint32(data[offset+16:])
			}
			offset += 24
		}
	}
	if s.bitsPerPixel != 16 && s.bitsPerPixel != 24 && s.bitsPerPixel != 32 {
		return x11Setup{}, fmt.Errorf("unsupported X11 pixel format: depth %d, %d bits per pixel", s.rootDepth, s.bitsPerPixel)
	}
	if s.redMask == 0 || s.greenMask == 0 || s.blueMask == 0 {
		return x11Setup{}, errors.New("X11 root visual is not TrueColor/DirectColor")
	}
	return s, nil
}

func x11Authority(displayNumber string) ([]byte, []byte) {
	path := os.Getenv("XAUTHORITY")
	if path == "" {
		if current, err := user.Current(); err == nil {
			path = filepath.Join(current.HomeDir, ".Xauthority")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	for len(data) >= 2 {
		_, data = takeXAuthField(data[2:]) // address
		number, rest := takeXAuthField(data)
		name, rest := takeXAuthField(rest)
		value, rest := takeXAuthField(rest)
		if rest == nil {
			break
		}
		if string(number) == displayNumber && string(name) == "MIT-MAGIC-COOKIE-1" {
			return name, value
		}
		data = rest
	}
	return nil, nil
}

func takeXAuthField(data []byte) ([]byte, []byte) {
	if len(data) < 2 {
		return nil, nil
	}
	n := int(binary.BigEndian.Uint16(data))
	if len(data) < 2+n {
		return nil, nil
	}
	return data[2 : 2+n], data[2+n:]
}

func aligned(value, alignment int) int {
	return (value + alignment - 1) / alignment * alignment
}

func u32Bytes(value uint32) []byte {
	result := make([]byte, 4)
	binary.LittleEndian.PutUint32(result, value)
	return result
}
