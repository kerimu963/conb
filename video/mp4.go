// Package video connects the container parser, H.264 decoder, and Canvas while
// keeping each layer independently reusable.
package video

import (
	"fmt"
	"io"
	"sort"
	"time"

	"conb/convert/h264"
	"conb/convert/mp4"
)

// MP4 is a decoded view of the first supported video track in an MP4 file.
type MP4 struct {
	Track    mp4.Track
	decoders map[uint32]*h264.Decoder
}

// OpenMP4 parses metadata and prepares AVC decoders without reading mdat.
func OpenMP4(source io.ReaderAt, size int64) (*MP4, error) {
	file, err := mp4.Parse(source, size)
	if err != nil {
		return nil, err
	}
	movie, err := file.Movie()
	if err != nil {
		return nil, err
	}
	for _, track := range movie.Tracks {
		if !track.IsVideo() || track.Samples.Len() == 0 {
			continue
		}
		stream := &MP4{Track: track, decoders: make(map[uint32]*h264.Decoder)}
		for index, entry := range track.SampleEntries {
			if entry.Format.String() != "avc1" && entry.Format.String() != "avc3" {
				continue
			}
			config, parseErr := h264.ParseAVCConfig(entry.DecoderConfig)
			if parseErr != nil {
				return nil, fmt.Errorf("sample description %d: %w", index+1, parseErr)
			}
			decoder, decoderErr := h264.NewDecoder(config)
			if decoderErr != nil {
				return nil, fmt.Errorf("sample description %d: %w", index+1, decoderErr)
			}
			stream.decoders[uint32(index+1)] = decoder
		}
		if len(stream.decoders) == 0 {
			return nil, fmt.Errorf("video track has no supported avc1/avc3 sample description")
		}
		return stream, nil
	}
	return nil, fmt.Errorf("MP4 has no non-empty video track")
}

func (m *MP4) Frames() int { return m.Track.Samples.Len() }

// PresentationOrder returns sample indices ordered by composition time. The
// decoder must still be called in increasing sample-index (decode-time) order.
func (m *MP4) PresentationOrder() []int {
	samples := m.Track.Samples.All()
	order := make([]int, len(samples))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool {
		return samples[order[i]].CompositionTime < samples[order[j]].CompositionTime
	})
	return order
}

// PresentationDuration returns how long an entry in PresentationOrder should
// remain visible. It uses adjacent composition timestamps and falls back to
// the sample duration for the final frame or a non-positive timestamp delta.
func (m *MP4) PresentationDuration(order []int, position int) (time.Duration, error) {
	if position < 0 || position >= len(order) || len(order) != m.Frames() {
		return 0, fmt.Errorf("presentation position %d is out of range", position)
	}
	sample, ok := m.Track.Samples.Sample(order[position])
	if !ok || m.Track.Timescale == 0 {
		return 0, fmt.Errorf("invalid presentation timing")
	}
	ticks := int64(sample.Duration)
	if position+1 < len(order) {
		next, nextOK := m.Track.Samples.Sample(order[position+1])
		if !nextOK {
			return 0, fmt.Errorf("invalid presentation order")
		}
		if delta := next.CompositionTime - sample.CompositionTime; delta > 0 {
			ticks = delta
		}
	}
	if ticks <= 0 {
		return 0, fmt.Errorf("sample %d has non-positive presentation duration", order[position])
	}
	return time.Duration(ticks * int64(time.Second) / int64(m.Track.Timescale)), nil
}

// FrameDuration returns the presentation duration recorded for a sample.
func (m *MP4) FrameDuration(index int) (time.Duration, error) {
	sample, ok := m.Track.Samples.Sample(index)
	if !ok {
		return 0, fmt.Errorf("sample %d is out of range", index)
	}
	if m.Track.Timescale == 0 {
		return 0, fmt.Errorf("video track has zero timescale")
	}
	return time.Duration(uint64(sample.Duration) * uint64(time.Second) / uint64(m.Track.Timescale)), nil
}

// Decode reads and decodes exactly one compressed sample.
func (m *MP4) Decode(index int) (*h264.Frame420, error) {
	sample, ok := m.Track.Samples.Sample(index)
	if !ok {
		return nil, fmt.Errorf("sample %d is out of range", index)
	}
	decoder, ok := m.decoders[sample.DescriptionIndex]
	if !ok {
		return nil, fmt.Errorf("sample %d uses unsupported description %d", index, sample.DescriptionIndex)
	}
	data, err := m.Track.Samples.Read(index)
	if err != nil {
		return nil, err
	}
	frame, err := decoder.DecodeSample(data)
	if err != nil {
		return nil, fmt.Errorf("sample %d: %w", index, err)
	}
	return frame, nil
}
