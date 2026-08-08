// Package h264 parses and decodes H.264/AVC bitstreams without external
// dependencies. It is independent from the MP4 container package.
package h264

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrMalformed = errors.New("malformed H.264 data")

// AVCConfig is an AVCDecoderConfigurationRecord stored in an MP4 avcC box.
type AVCConfig struct {
	Profile         uint8
	Compatibility   uint8
	Level           uint8
	NALLengthSize   int
	SequenceHeaders [][]byte
	PictureHeaders  [][]byte
	TrailingData    []byte
}

// ParseAVCConfig parses an avcC payload.
func ParseAVCConfig(data []byte) (AVCConfig, error) {
	if len(data) < 7 {
		return AVCConfig{}, malformed("avcC is truncated")
	}
	if data[0] != 1 {
		return AVCConfig{}, malformed(fmt.Sprintf("unsupported avcC version %d", data[0]))
	}
	config := AVCConfig{
		Profile:       data[1],
		Compatibility: data[2],
		Level:         data[3],
		NALLengthSize: int(data[4]&3) + 1,
	}
	if config.NALLengthSize == 3 {
		return AVCConfig{}, malformed("avcC uses the reserved 3-byte NAL length size")
	}
	offset := 6
	var err error
	config.SequenceHeaders, offset, err = readParameterSets(data, offset, int(data[5]&31), "SPS")
	if err != nil {
		return AVCConfig{}, err
	}
	if offset >= len(data) {
		return AVCConfig{}, malformed("avcC has no PPS count")
	}
	ppsCount := int(data[offset])
	offset++
	config.PictureHeaders, offset, err = readParameterSets(data, offset, ppsCount, "PPS")
	if err != nil {
		return AVCConfig{}, err
	}
	config.TrailingData = append([]byte(nil), data[offset:]...)
	return config, nil
}

func readParameterSets(data []byte, offset, count int, kind string) ([][]byte, int, error) {
	result := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		if offset+2 > len(data) {
			return nil, 0, malformed(kind + " length is truncated")
		}
		size := int(binary.BigEndian.Uint16(data[offset:]))
		offset += 2
		if size == 0 || offset+size > len(data) {
			return nil, 0, malformed(kind + " data is truncated or empty")
		}
		result = append(result, append([]byte(nil), data[offset:offset+size]...))
		offset += size
	}
	return result, offset, nil
}

func malformed(message string) error { return fmt.Errorf("%w: %s", ErrMalformed, message) }
