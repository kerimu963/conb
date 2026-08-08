package h264

import (
	"encoding/binary"
	"fmt"
)

// NALType is the five-bit nal_unit_type value.
type NALType uint8

const (
	NALSliceNonIDR NALType = 1
	NALSliceIDR    NALType = 5
	NALSEI         NALType = 6
	NALSPS         NALType = 7
	NALPPS         NALType = 8
	NALAccessUnit  NALType = 9
)

// NALUnit contains a complete NAL unit and its decoded header fields.
type NALUnit struct {
	RefIDC uint8
	Type   NALType
	Data   []byte
}

// ParseNALUnit parses one complete NAL unit, including its one-byte header.
func ParseNALUnit(data []byte) (NALUnit, error) { return newNALUnit(data) }

// Payload returns the EBSP bytes following the one-byte NAL header.
func (n NALUnit) Payload() []byte {
	if len(n.Data) < 1 {
		return nil
	}
	return n.Data[1:]
}

// ParseSample splits an MP4 length-prefixed AVC sample into NAL units.
func ParseSample(data []byte, lengthSize int) ([]NALUnit, error) {
	if lengthSize < 1 || lengthSize > 4 || lengthSize == 3 {
		return nil, fmt.Errorf("%w: invalid NAL length size %d", ErrMalformed, lengthSize)
	}
	result := make([]NALUnit, 0)
	for offset := 0; offset < len(data); {
		if offset+lengthSize > len(data) {
			return nil, malformed("NAL length is truncated")
		}
		var size uint32
		switch lengthSize {
		case 1:
			size = uint32(data[offset])
		case 2:
			size = uint32(binary.BigEndian.Uint16(data[offset:]))
		case 4:
			size = binary.BigEndian.Uint32(data[offset:])
		}
		offset += lengthSize
		if size == 0 || uint64(size) > uint64(len(data)-offset) {
			return nil, malformed("NAL payload is truncated or empty")
		}
		unit, err := newNALUnit(data[offset : offset+int(size)])
		if err != nil {
			return nil, err
		}
		result = append(result, unit)
		offset += int(size)
	}
	return result, nil
}

// ParseAnnexB splits a start-code-delimited byte stream into NAL units.
func ParseAnnexB(data []byte) ([]NALUnit, error) {
	start, prefix := findStartCode(data, 0)
	if start < 0 {
		return nil, malformed("Annex B stream has no start code")
	}
	for _, value := range data[:start] {
		if value != 0 {
			return nil, malformed("non-zero data before Annex B start code")
		}
	}
	result := make([]NALUnit, 0)
	start += prefix
	for {
		next, nextPrefix := findStartCode(data, start)
		end := len(data)
		if next >= 0 {
			end = next
		}
		for end > start && data[end-1] == 0 {
			end--
		}
		if end > start {
			unit, err := newNALUnit(data[start:end])
			if err != nil {
				return nil, err
			}
			result = append(result, unit)
		}
		if next < 0 {
			break
		}
		start = next + nextPrefix
	}
	if len(result) == 0 {
		return nil, malformed("Annex B stream has no NAL units")
	}
	return result, nil
}

// AnnexB serializes NAL units with four-byte start codes.
func AnnexB(units []NALUnit) []byte {
	size := 0
	for _, unit := range units {
		size += 4 + len(unit.Data)
	}
	result := make([]byte, 0, size)
	for _, unit := range units {
		result = append(result, 0, 0, 0, 1)
		result = append(result, unit.Data...)
	}
	return result
}

func newNALUnit(data []byte) (NALUnit, error) {
	if len(data) == 0 {
		return NALUnit{}, malformed("empty NAL unit")
	}
	if data[0]&0x80 != 0 {
		return NALUnit{}, malformed("NAL forbidden_zero_bit is set")
	}
	unitType := NALType(data[0] & 0x1f)
	if unitType == 0 {
		return NALUnit{}, malformed("NAL unit type is zero")
	}
	return NALUnit{RefIDC: data[0] >> 5 & 3, Type: unitType, Data: append([]byte(nil), data...)}, nil
}

func findStartCode(data []byte, offset int) (int, int) {
	for i := offset; i+3 <= len(data); i++ {
		if data[i] == 0 && data[i+1] == 0 {
			if data[i+2] == 1 {
				return i, 3
			}
			if i+4 <= len(data) && data[i+2] == 0 && data[i+3] == 1 {
				return i, 4
			}
		}
	}
	return -1, 0
}
