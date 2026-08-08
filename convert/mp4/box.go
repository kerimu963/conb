package mp4

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	// ErrMalformed indicates invalid or truncated MP4 container data.
	ErrMalformed = errors.New("malformed MP4")
	// ErrNotFound indicates that a requested box or track is absent.
	ErrNotFound = errors.New("MP4 item not found")
)

// Box describes one MP4 box. Offset and Size include its header.
type Box struct {
	Type       FourCC
	Offset     int64
	Size       uint64
	HeaderSize uint64
	reader     io.ReaderAt
}

// PayloadSize returns the number of bytes following the box header.
func (b Box) PayloadSize() uint64 { return b.Size - b.HeaderSize }

// Payload returns a bounded reader for the box payload.
func (b Box) Payload() *io.SectionReader {
	return io.NewSectionReader(b.reader, b.Offset+int64(b.HeaderSize), int64(b.PayloadSize()))
}

// Children parses the payload as a sequence of child boxes.
func (b Box) Children() ([]Box, error) {
	return readBoxes(b.reader, b.Offset+int64(b.HeaderSize), int64(b.PayloadSize()))
}

// FullBoxHeader is shared by MP4 boxes whose payload begins with a version and
// 24-bit flags field.
type FullBoxHeader struct {
	Version uint8
	Flags   uint32
}

func readFullBoxHeader(r io.Reader) (FullBoxHeader, error) {
	var raw [4]byte
	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return FullBoxHeader{}, malformed("full box header", err)
	}
	return FullBoxHeader{
		Version: raw[0],
		Flags:   uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3]),
	}, nil
}

func readBoxes(r io.ReaderAt, offset, length int64) ([]Box, error) {
	if offset < 0 || length < 0 {
		return nil, malformed("negative box range", nil)
	}
	end := offset + length
	if end < offset {
		return nil, malformed("box range overflow", nil)
	}
	boxes := make([]Box, 0)
	for offset < end {
		remaining := end - offset
		if remaining < 8 {
			return nil, malformed(fmt.Sprintf("%d trailing bytes at offset %d", remaining, offset), nil)
		}
		var header [16]byte
		if _, err := r.ReadAt(header[:8], offset); err != nil {
			return nil, malformed(fmt.Sprintf("box header at offset %d", offset), err)
		}
		size := uint64(binary.BigEndian.Uint32(header[:4]))
		headerSize := uint64(8)
		if size == 1 {
			if remaining < 16 {
				return nil, malformed(fmt.Sprintf("large box header at offset %d", offset), io.ErrUnexpectedEOF)
			}
			if _, err := r.ReadAt(header[8:16], offset+8); err != nil {
				return nil, malformed(fmt.Sprintf("large box header at offset %d", offset), err)
			}
			size = binary.BigEndian.Uint64(header[8:16])
			headerSize = 16
		} else if size == 0 {
			size = uint64(remaining)
		}
		if size < headerSize {
			return nil, malformed(fmt.Sprintf("box %q at offset %d has size %d", string(header[4:8]), offset, size), nil)
		}
		if size > uint64(remaining) {
			return nil, malformed(fmt.Sprintf("box %q at offset %d extends past its parent", string(header[4:8]), offset), io.ErrUnexpectedEOF)
		}
		box := Box{Offset: offset, Size: size, HeaderSize: headerSize, reader: r}
		copy(box.Type[:], header[4:8])
		boxes = append(boxes, box)
		offset += int64(size)
	}
	return boxes, nil
}

func malformed(context string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrMalformed, context)
	}
	return fmt.Errorf("%w: %s: %v", ErrMalformed, context, cause)
}
