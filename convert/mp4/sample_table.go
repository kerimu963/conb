package mp4

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Sample describes one compressed media access unit in the MP4 file.
// DecodeTime and CompositionTime use the containing Track's timescale.
type Sample struct {
	Offset           uint64
	Size             uint32
	DecodeTime       uint64
	CompositionTime  int64
	Duration         uint32
	DescriptionIndex uint32
	IsSync           bool
}

// SampleTable maps logical media samples to byte ranges in the source file.
type SampleTable struct {
	reader  io.ReaderAt
	samples []Sample
}

func (t SampleTable) Len() int { return len(t.samples) }

// Sample returns metadata for a zero-based sample index.
func (t SampleTable) Sample(index int) (Sample, bool) {
	if index < 0 || index >= len(t.samples) {
		return Sample{}, false
	}
	return t.samples[index], true
}

// All returns a copy of all sample metadata.
func (t SampleTable) All() []Sample { return append([]Sample(nil), t.samples...) }

// Read reads one compressed sample without loading the rest of mdat.
func (t SampleTable) Read(index int) ([]byte, error) {
	sample, ok := t.Sample(index)
	if !ok {
		return nil, fmt.Errorf("%w: sample %d", ErrNotFound, index)
	}
	if uint64(int64(sample.Offset)) != sample.Offset {
		return nil, malformed("sample offset exceeds ReaderAt range", nil)
	}
	if uint64(sample.Size) > uint64(maxInt()) {
		return nil, malformed("sample size exceeds memory range", nil)
	}
	data := make([]byte, int(sample.Size))
	if _, err := t.reader.ReadAt(data, int64(sample.Offset)); err != nil {
		return nil, malformed(fmt.Sprintf("sample %d data", index), err)
	}
	return data, nil
}

type timeToSample struct{ count, delta uint32 }
type compositionOffset struct {
	count  uint32
	offset int64
}
type sampleToChunk struct{ firstChunk, samplesPerChunk, descriptionIndex uint32 }

func parseSamples(reader io.ReaderAt, boxes []Box) (SampleTable, error) {
	sttsBox, ok := findBox(boxes, Type("stts"))
	_, hasSTSC := findBox(boxes, Type("stsc"))
	_, hasSTSZ := findBox(boxes, Type("stsz"))
	_, hasSTCO := findBox(boxes, Type("stco"))
	_, hasCO64 := findBox(boxes, Type("co64"))
	if !ok && !hasSTSC && !hasSTSZ && !hasSTCO && !hasCO64 {
		// Fragmented MP4 files may leave the classic sample table empty and
		// describe their samples in moof/traf instead.
		return SampleTable{reader: reader}, nil
	}
	if !ok {
		return SampleTable{}, malformed("stbl has no stts", nil)
	}
	stscBox, ok := findBox(boxes, Type("stsc"))
	if !ok {
		return SampleTable{}, malformed("stbl has no stsc", nil)
	}
	stszBox, ok := findBox(boxes, Type("stsz"))
	if !ok {
		return SampleTable{}, malformed("stbl has no stsz", nil)
	}
	stcoBox, hasSTCO := findBox(boxes, Type("stco"))
	co64Box, hasCO64 := findBox(boxes, Type("co64"))
	if hasSTCO == hasCO64 {
		return SampleTable{}, malformed("stbl must contain exactly one of stco or co64", nil)
	}

	timing, err := parseSTTS(sttsBox)
	if err != nil {
		return SampleTable{}, err
	}
	chunkMap, err := parseSTSC(stscBox)
	if err != nil {
		return SampleTable{}, err
	}
	sizes, err := parseSTSZ(stszBox)
	if err != nil {
		return SampleTable{}, err
	}
	var chunks []uint64
	if hasSTCO {
		chunks, err = parseChunkOffsets(stcoBox, 4)
	} else {
		chunks, err = parseChunkOffsets(co64Box, 8)
	}
	if err != nil {
		return SampleTable{}, err
	}

	var compositions []compositionOffset
	if box, found := findBox(boxes, Type("ctts")); found {
		compositions, err = parseCTTS(box)
		if err != nil {
			return SampleTable{}, err
		}
	}
	var sync map[uint32]struct{}
	if box, found := findBox(boxes, Type("stss")); found {
		sync, err = parseSTSS(box, len(sizes))
		if err != nil {
			return SampleTable{}, err
		}
	}

	samples := make([]Sample, len(sizes))
	if err := applyChunks(samples, sizes, chunks, chunkMap); err != nil {
		return SampleTable{}, err
	}
	if err := applyTiming(samples, timing, compositions); err != nil {
		return SampleTable{}, err
	}
	for i := range samples {
		if sync == nil {
			samples[i].IsSync = true
		} else {
			_, samples[i].IsSync = sync[uint32(i+1)]
		}
	}
	return SampleTable{reader: reader, samples: samples}, nil
}

func parseSTTS(box Box) ([]timeToSample, error) {
	data, count, err := fullBoxEntries(box, 8)
	if err != nil {
		return nil, err
	}
	result := make([]timeToSample, count)
	for i := range result {
		offset := 8 + i*8
		result[i] = timeToSample{binary.BigEndian.Uint32(data[offset:]), binary.BigEndian.Uint32(data[offset+4:])}
		if result[i].count == 0 {
			return nil, malformed("stts contains a zero sample count", nil)
		}
	}
	return result, nil
}

func parseCTTS(box Box) ([]compositionOffset, error) {
	data, count, err := fullBoxEntries(box, 8)
	if err != nil {
		return nil, err
	}
	if data[0] > 1 {
		return nil, malformed(fmt.Sprintf("unsupported ctts version %d", data[0]), nil)
	}
	result := make([]compositionOffset, count)
	for i := range result {
		offset := 8 + i*8
		result[i].count = binary.BigEndian.Uint32(data[offset:])
		raw := binary.BigEndian.Uint32(data[offset+4:])
		if data[0] == 1 {
			result[i].offset = int64(int32(raw))
		} else {
			result[i].offset = int64(raw)
		}
		if result[i].count == 0 {
			return nil, malformed("ctts contains a zero sample count", nil)
		}
	}
	return result, nil
}

func parseSTSC(box Box) ([]sampleToChunk, error) {
	data, count, err := fullBoxEntries(box, 12)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, malformed("stsc has no entries", nil)
	}
	result := make([]sampleToChunk, count)
	for i := range result {
		offset := 8 + i*12
		result[i] = sampleToChunk{
			firstChunk:       binary.BigEndian.Uint32(data[offset:]),
			samplesPerChunk:  binary.BigEndian.Uint32(data[offset+4:]),
			descriptionIndex: binary.BigEndian.Uint32(data[offset+8:]),
		}
		if result[i].firstChunk == 0 || result[i].samplesPerChunk == 0 || result[i].descriptionIndex == 0 ||
			(i > 0 && result[i].firstChunk <= result[i-1].firstChunk) {
			return nil, malformed("invalid stsc entry", nil)
		}
	}
	if result[0].firstChunk != 1 {
		return nil, malformed("stsc first entry does not start at chunk 1", nil)
	}
	return result, nil
}

func parseSTSZ(box Box) ([]uint32, error) {
	data, err := boxPayload(box)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 {
		return nil, malformed("stsz is truncated", nil)
	}
	defaultSize := binary.BigEndian.Uint32(data[4:])
	count := binary.BigEndian.Uint32(data[8:])
	if uint64(count) > uint64(maxInt()) {
		return nil, malformed("stsz sample count is too large", nil)
	}
	result := make([]uint32, int(count))
	if defaultSize != 0 {
		for i := range result {
			result[i] = defaultSize
		}
		return result, nil
	}
	if uint64(len(data)) != 12+uint64(count)*4 {
		return nil, malformed("stsz entry count does not match its size", nil)
	}
	for i := range result {
		result[i] = binary.BigEndian.Uint32(data[12+i*4:])
	}
	return result, nil
}

func parseChunkOffsets(box Box, width int) ([]uint64, error) {
	data, count, err := fullBoxEntries(box, width)
	if err != nil {
		return nil, err
	}
	result := make([]uint64, count)
	for i := range result {
		offset := 8 + i*width
		if width == 4 {
			result[i] = uint64(binary.BigEndian.Uint32(data[offset:]))
		} else {
			result[i] = binary.BigEndian.Uint64(data[offset:])
		}
	}
	return result, nil
}

func parseSTSS(box Box, sampleCount int) (map[uint32]struct{}, error) {
	data, count, err := fullBoxEntries(box, 4)
	if err != nil {
		return nil, err
	}
	result := make(map[uint32]struct{}, count)
	for i := 0; i < count; i++ {
		index := binary.BigEndian.Uint32(data[8+i*4:])
		if index == 0 || uint64(index) > uint64(sampleCount) {
			return nil, malformed("stss sample number is out of range", nil)
		}
		result[index] = struct{}{}
	}
	return result, nil
}

func fullBoxEntries(box Box, entrySize int) ([]byte, int, error) {
	data, err := boxPayload(box)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < 8 {
		return nil, 0, malformed(box.Type.String()+" is truncated", nil)
	}
	count := binary.BigEndian.Uint32(data[4:])
	if uint64(count) > uint64(maxInt()) || uint64(len(data)) != 8+uint64(count)*uint64(entrySize) {
		return nil, 0, malformed(box.Type.String()+" entry count does not match its size", nil)
	}
	return data, int(count), nil
}

func applyChunks(samples []Sample, sizes []uint32, chunks []uint64, mapping []sampleToChunk) error {
	sampleIndex, mapIndex := 0, 0
	for chunkIndex, chunkOffset := range chunks {
		chunkNumber := uint32(chunkIndex + 1)
		if mapIndex+1 < len(mapping) && chunkNumber >= mapping[mapIndex+1].firstChunk {
			mapIndex++
		}
		entry := mapping[mapIndex]
		offset := chunkOffset
		for range entry.samplesPerChunk {
			if sampleIndex >= len(samples) {
				return malformed("stsc maps more samples than stsz", nil)
			}
			samples[sampleIndex].Offset = offset
			samples[sampleIndex].Size = sizes[sampleIndex]
			samples[sampleIndex].DescriptionIndex = entry.descriptionIndex
			if ^uint64(0)-offset < uint64(sizes[sampleIndex]) {
				return malformed("sample offset overflow", nil)
			}
			offset += uint64(sizes[sampleIndex])
			sampleIndex++
		}
	}
	if sampleIndex != len(samples) {
		return malformed("stsc maps fewer samples than stsz", nil)
	}
	return nil
}

func applyTiming(samples []Sample, timing []timeToSample, composition []compositionOffset) error {
	index := 0
	var decodeTime uint64
	for _, entry := range timing {
		for range entry.count {
			if index >= len(samples) {
				return malformed("stts describes more samples than stsz", nil)
			}
			samples[index].DecodeTime = decodeTime
			samples[index].Duration = entry.delta
			samples[index].CompositionTime = int64(decodeTime)
			if ^uint64(0)-decodeTime < uint64(entry.delta) {
				return malformed("decode timestamp overflow", nil)
			}
			decodeTime += uint64(entry.delta)
			index++
		}
	}
	if index != len(samples) {
		return malformed("stts describes fewer samples than stsz", nil)
	}
	if composition == nil {
		return nil
	}
	index = 0
	for _, entry := range composition {
		for range entry.count {
			if index >= len(samples) {
				return malformed("ctts describes more samples than stsz", nil)
			}
			if samples[index].DecodeTime > uint64(1<<63-1) {
				return malformed("decode timestamp exceeds signed composition range", nil)
			}
			decode := int64(samples[index].DecodeTime)
			if entry.offset > 0 && decode > int64(1<<63-1)-entry.offset {
				return malformed("composition timestamp overflow", nil)
			}
			samples[index].CompositionTime = decode + entry.offset
			index++
		}
	}
	if index != len(samples) {
		return malformed("ctts describes fewer samples than stsz", nil)
	}
	return nil
}

func maxInt() int { return int(^uint(0) >> 1) }
