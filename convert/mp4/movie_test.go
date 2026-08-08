package mp4

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestMovieVideoTrack(t *testing.T) {
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:], 1000)
	binary.BigEndian.PutUint32(mvhd[16:], 2500)

	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[12:], 7)
	binary.BigEndian.PutUint32(tkhd[76:], 640<<16)
	binary.BigEndian.PutUint32(tkhd[80:], 360<<16)

	mdhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mdhd[12:], 90000)
	binary.BigEndian.PutUint32(mdhd[16:], 225000)
	hdlr := make([]byte, 12)
	copy(hdlr[8:], "vide")

	avcCData := []byte{1, 100, 0, 40, 0xff}
	visual := make([]byte, 78)
	binary.BigEndian.PutUint16(visual[6:], 1)
	binary.BigEndian.PutUint16(visual[24:], 640)
	binary.BigEndian.PutUint16(visual[26:], 360)
	visual = append(visual, box("avcC", avcCData)...)
	stsdPayload := make([]byte, 8)
	binary.BigEndian.PutUint32(stsdPayload[4:], 1)
	stsdPayload = append(stsdPayload, box("avc1", visual)...)

	stbl := box("stbl", box("stsd", stsdPayload))
	minf := box("minf", stbl)
	mdia := box("mdia", append(append(box("mdhd", mdhd), box("hdlr", hdlr)...), minf...))
	trak := box("trak", append(box("tkhd", tkhd), mdia...))
	moovPayload := append(box("mvhd", mvhd), trak...)
	data := box("moov", moovPayload)

	file, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	movie, err := file.Movie()
	if err != nil {
		t.Fatal(err)
	}
	if movie.Timescale != 1000 || movie.Duration != 2500 || movie.DurationTime() != 2500*time.Millisecond {
		t.Fatalf("movie timing = %+v", movie)
	}
	if len(movie.Tracks) != 1 {
		t.Fatalf("track count = %d, want 1", len(movie.Tracks))
	}
	track := movie.Tracks[0]
	if track.ID != 7 || !track.IsVideo() || track.Width != 640 || track.Height != 360 {
		t.Fatalf("track = %+v", track)
	}
	if len(track.SampleEntries) != 1 {
		t.Fatalf("sample entries = %v", track.SampleEntries)
	}
	entry := track.SampleEntries[0]
	if entry.Format != Type("avc1") || entry.ConfigType != Type("avcC") ||
		entry.Width != 640 || entry.Height != 360 || !bytes.Equal(entry.DecoderConfig, avcCData) {
		t.Fatalf("sample entry = %+v", entry)
	}
}
