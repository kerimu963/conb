package mp4

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// Movie contains container-level timing and track metadata.
type Movie struct {
	Timescale uint32
	Duration  uint64
	Tracks    []Track
}

// DurationTime converts the movie duration to Go's time.Duration.
func (m Movie) DurationTime() time.Duration {
	return scaledDuration(m.Duration, m.Timescale)
}

// Track describes one MP4 media track.
type Track struct {
	ID            uint32
	Handler       FourCC
	Timescale     uint32
	Duration      uint64
	Width, Height uint32
	SampleEntries []SampleEntry
	Samples       SampleTable
}

// IsVideo reports whether this is a vide handler track.
func (t Track) IsVideo() bool { return t.Handler == Type("vide") }

// DurationTime converts the media duration to Go's time.Duration.
func (t Track) DurationTime() time.Duration {
	return scaledDuration(t.Duration, t.Timescale)
}

// SampleEntry describes a codec sample description from stsd. DecoderConfig
// contains avcC/hvcC/etc. payload bytes when present.
type SampleEntry struct {
	Format        FourCC
	DataReference uint16
	Width, Height uint16
	ConfigType    FourCC
	DecoderConfig []byte
}

// Movie parses moov and its tracks without reading media samples.
func (f *File) Movie() (Movie, error) {
	moov, ok := f.Find(Type("moov"))
	if !ok {
		return Movie{}, fmt.Errorf("%w: moov", ErrNotFound)
	}
	children, err := moov.Children()
	if err != nil {
		return Movie{}, err
	}
	var movie Movie
	for _, child := range children {
		switch child.Type.String() {
		case "mvhd":
			movie.Timescale, movie.Duration, err = parseMediaHeader(child)
			if err != nil {
				return Movie{}, err
			}
		case "trak":
			track, trackErr := parseTrack(child)
			if trackErr != nil {
				return Movie{}, trackErr
			}
			movie.Tracks = append(movie.Tracks, track)
		}
	}
	if movie.Timescale == 0 {
		return Movie{}, malformed("moov has no valid mvhd", nil)
	}
	return movie, nil
}

func parseTrack(trak Box) (Track, error) {
	children, err := trak.Children()
	if err != nil {
		return Track{}, err
	}
	var track Track
	var haveHeader, haveMedia bool
	for _, child := range children {
		switch child.Type.String() {
		case "tkhd":
			track.ID, track.Width, track.Height, err = parseTrackHeader(child)
			haveHeader = err == nil
		case "mdia":
			err = parseMedia(child, &track)
			haveMedia = err == nil
		}
		if err != nil {
			return Track{}, err
		}
	}
	if !haveHeader || !haveMedia {
		return Track{}, malformed("trak is missing tkhd or mdia", nil)
	}
	return track, nil
}

func parseMedia(mdia Box, track *Track) error {
	children, err := mdia.Children()
	if err != nil {
		return err
	}
	for _, child := range children {
		switch child.Type.String() {
		case "mdhd":
			track.Timescale, track.Duration, err = parseMediaHeader(child)
		case "hdlr":
			track.Handler, err = parseHandler(child)
		case "minf":
			track.SampleEntries, track.Samples, err = parseSampleTable(child)
		}
		if err != nil {
			return err
		}
	}
	if track.Timescale == 0 || track.Handler == (FourCC{}) {
		return malformed("mdia is missing mdhd or hdlr", nil)
	}
	return nil
}

func parseTrackHeader(box Box) (id, width, height uint32, err error) {
	data, err := boxPayload(box)
	if err != nil {
		return 0, 0, 0, err
	}
	if len(data) < 84 {
		return 0, 0, 0, malformed("tkhd is truncated", nil)
	}
	version := data[0]
	idOffset := 12
	minimum := 84
	if version == 1 {
		idOffset = 20
		minimum = 96
	} else if version != 0 {
		return 0, 0, 0, malformed(fmt.Sprintf("unsupported tkhd version %d", version), nil)
	}
	if len(data) < minimum {
		return 0, 0, 0, malformed("tkhd is truncated", nil)
	}
	id = binary.BigEndian.Uint32(data[idOffset:])
	width = binary.BigEndian.Uint32(data[len(data)-8:]) >> 16
	height = binary.BigEndian.Uint32(data[len(data)-4:]) >> 16
	return id, width, height, nil
}

func parseMediaHeader(box Box) (timescale uint32, duration uint64, err error) {
	data, err := boxPayload(box)
	if err != nil {
		return 0, 0, err
	}
	if len(data) < 20 {
		return 0, 0, malformed(box.Type.String()+" is truncated", nil)
	}
	switch data[0] {
	case 0:
		timescale = binary.BigEndian.Uint32(data[12:])
		duration = uint64(binary.BigEndian.Uint32(data[16:]))
	case 1:
		if len(data) < 32 {
			return 0, 0, malformed(box.Type.String()+" version 1 is truncated", nil)
		}
		timescale = binary.BigEndian.Uint32(data[20:])
		duration = binary.BigEndian.Uint64(data[24:])
	default:
		return 0, 0, malformed(fmt.Sprintf("unsupported %s version %d", box.Type, data[0]), nil)
	}
	if timescale == 0 {
		return 0, 0, malformed(box.Type.String()+" has zero timescale", nil)
	}
	return timescale, duration, nil
}

func parseHandler(box Box) (FourCC, error) {
	data, err := boxPayload(box)
	if err != nil {
		return FourCC{}, err
	}
	if len(data) < 12 {
		return FourCC{}, malformed("hdlr is truncated", nil)
	}
	var handler FourCC
	copy(handler[:], data[8:12])
	return handler, nil
}

func parseSampleTable(minf Box) ([]SampleEntry, SampleTable, error) {
	children, err := minf.Children()
	if err != nil {
		return nil, SampleTable{}, err
	}
	stbl, ok := findBox(children, Type("stbl"))
	if !ok {
		return nil, SampleTable{}, malformed("minf has no stbl", nil)
	}
	children, err = stbl.Children()
	if err != nil {
		return nil, SampleTable{}, err
	}
	stsd, ok := findBox(children, Type("stsd"))
	if !ok {
		return nil, SampleTable{}, nil
	}
	entries, err := parseSampleDescriptions(stsd)
	if err != nil {
		return nil, SampleTable{}, err
	}
	table, err := parseSamples(stbl.reader, children)
	if err != nil {
		return nil, SampleTable{}, err
	}
	for _, sample := range table.samples {
		if sample.DescriptionIndex == 0 || uint64(sample.DescriptionIndex) > uint64(len(entries)) {
			return nil, SampleTable{}, malformed("sample description index is out of range", nil)
		}
	}
	return entries, table, nil
}

func parseSampleDescriptions(stsd Box) ([]SampleEntry, error) {
	data, err := boxPayload(stsd)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, malformed("stsd is truncated", nil)
	}
	count := binary.BigEndian.Uint32(data[4:8])
	entries, err := readBoxes(stsd.reader, stsd.Offset+int64(stsd.HeaderSize)+8, int64(len(data)-8))
	if err != nil {
		return nil, err
	}
	if uint32(len(entries)) != count {
		return nil, malformed(fmt.Sprintf("stsd declares %d entries but contains %d", count, len(entries)), nil)
	}
	result := make([]SampleEntry, 0, len(entries))
	for _, entryBox := range entries {
		entryData, readErr := boxPayload(entryBox)
		if readErr != nil {
			return nil, readErr
		}
		if len(entryData) < 8 {
			return nil, malformed("sample entry is truncated", nil)
		}
		entry := SampleEntry{
			Format:        entryBox.Type,
			DataReference: binary.BigEndian.Uint16(entryData[6:8]),
		}
		// ISO visual sample entries have a 78-byte fixed payload followed by
		// codec-specific child boxes such as avcC.
		if isVisualSampleEntry(entry.Format) {
			if len(entryData) < 78 {
				return nil, malformed("visual sample entry is truncated", nil)
			}
			entry.Width = binary.BigEndian.Uint16(entryData[24:26])
			entry.Height = binary.BigEndian.Uint16(entryData[26:28])
			configBoxes, configErr := readBoxes(entryBox.reader, entryBox.Offset+int64(entryBox.HeaderSize)+78, int64(len(entryData)-78))
			if configErr != nil {
				return nil, configErr
			}
			for _, config := range configBoxes {
				if config.Type == Type("avcC") || config.Type == Type("hvcC") || config.Type == Type("vpcC") || config.Type == Type("av1C") {
					entry.ConfigType = config.Type
					entry.DecoderConfig, readErr = boxPayload(config)
					if readErr != nil {
						return nil, readErr
					}
					break
				}
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

func isVisualSampleEntry(format FourCC) bool {
	switch format.String() {
	case "avc1", "avc3", "hvc1", "hev1", "vp09", "av01":
		return true
	default:
		return false
	}
}

func boxPayload(box Box) ([]byte, error) {
	if box.PayloadSize() > uint64(^uint(0)>>1) {
		return nil, malformed(box.Type.String()+" payload is too large", nil)
	}
	data := make([]byte, int(box.PayloadSize()))
	if _, err := io.ReadFull(box.Payload(), data); err != nil {
		return nil, malformed(box.Type.String()+" payload", err)
	}
	return data, nil
}

func findBox(boxes []Box, boxType FourCC) (Box, bool) {
	for _, box := range boxes {
		if box.Type == boxType {
			return box, true
		}
	}
	return Box{}, false
}

func scaledDuration(value uint64, timescale uint32) time.Duration {
	if timescale == 0 {
		return 0
	}
	seconds := value / uint64(timescale)
	fraction := value % uint64(timescale)
	if seconds > uint64((1<<63-1)/int64(time.Second)) {
		return time.Duration(1<<63 - 1)
	}
	return time.Duration(seconds)*time.Second + time.Duration(fraction*uint64(time.Second)/uint64(timescale))
}
