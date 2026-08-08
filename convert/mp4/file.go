package mp4

import (
	"encoding/binary"
	"fmt"
	"io"
)

// File is a parsed MP4 container. Media payloads remain in the original
// ReaderAt and are read on demand.
type File struct {
	reader io.ReaderAt
	size   int64
	boxes  []Box
}

// Parse reads the top-level box index. It does not load mdat payloads into
// memory, making it suitable for large video files.
func Parse(reader io.ReaderAt, size int64) (*File, error) {
	if reader == nil {
		return nil, fmt.Errorf("%w: nil reader", ErrMalformed)
	}
	if size < 0 {
		return nil, fmt.Errorf("%w: negative file size", ErrMalformed)
	}
	boxes, err := readBoxes(reader, 0, size)
	if err != nil {
		return nil, err
	}
	return &File{reader: reader, size: size, boxes: boxes}, nil
}

// Boxes returns a copy of the top-level box index.
func (f *File) Boxes() []Box { return append([]Box(nil), f.boxes...) }

// Find returns the first top-level box of the requested type.
func (f *File) Find(boxType FourCC) (Box, bool) {
	for _, box := range f.boxes {
		if box.Type == boxType {
			return box, true
		}
	}
	return Box{}, false
}

// FileType contains the decoded ftyp box.
type FileType struct {
	MajorBrand       FourCC
	MinorVersion     uint32
	CompatibleBrands []FourCC
}

// FileType parses the optional top-level ftyp box.
func (f *File) FileType() (FileType, error) {
	box, ok := f.Find(Type("ftyp"))
	if !ok {
		return FileType{}, fmt.Errorf("%w: ftyp", ErrNotFound)
	}
	if box.PayloadSize() < 8 || (box.PayloadSize()-8)%4 != 0 {
		return FileType{}, malformed("invalid ftyp payload size", nil)
	}
	payload := make([]byte, box.PayloadSize())
	if _, err := io.ReadFull(box.Payload(), payload); err != nil {
		return FileType{}, malformed("ftyp payload", err)
	}
	result := FileType{MinorVersion: binary.BigEndian.Uint32(payload[4:8])}
	copy(result.MajorBrand[:], payload[:4])
	for offset := 8; offset < len(payload); offset += 4 {
		var brand FourCC
		copy(brand[:], payload[offset:offset+4])
		result.CompatibleBrands = append(result.CompatibleBrands, brand)
	}
	return result, nil
}
