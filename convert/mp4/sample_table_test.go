package mp4

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSampleTableMapsOffsetsTimesAndSyncSamples(t *testing.T) {
	stts := tableBox("stts", 0, [][]uint32{{3, 100}})
	stsc := tableBox("stsc", 0, [][]uint32{{1, 2, 1}, {2, 1, 1}})
	stszPayload := make([]byte, 12+3*4)
	binary.BigEndian.PutUint32(stszPayload[8:], 3)
	for i, size := range []uint32{3, 2, 4} {
		binary.BigEndian.PutUint32(stszPayload[12+i*4:], size)
	}
	stco := tableBox("stco", 0, [][]uint32{{300}, {350}})
	ctts := tableBox("ctts", 1, [][]uint32{{1, 0xfffffff6}, {2, 5}})
	stss := tableBox("stss", 0, [][]uint32{{1}, {3}})
	stblPayload := append(append(append(append(append(stts, stsc...), box("stsz", stszPayload)...), stco...), ctts...), stss...)
	stbl := box("stbl", stblPayload)

	data := make([]byte, 400)
	copy(data, stbl)
	copy(data[300:], []byte{1, 2, 3, 4, 5})
	copy(data[350:], []byte{6, 7, 8, 9})
	file, err := Parse(bytes.NewReader(data), int64(len(stbl)))
	if err != nil {
		t.Fatal(err)
	}
	children, err := file.Boxes()[0].Children()
	if err != nil {
		t.Fatal(err)
	}
	table, err := parseSamples(bytes.NewReader(data), children)
	if err != nil {
		t.Fatal(err)
	}
	if table.Len() != 3 {
		t.Fatalf("sample count = %d, want 3", table.Len())
	}
	wants := []Sample{
		{Offset: 300, Size: 3, DecodeTime: 0, CompositionTime: -10, Duration: 100, DescriptionIndex: 1, IsSync: true},
		{Offset: 303, Size: 2, DecodeTime: 100, CompositionTime: 105, Duration: 100, DescriptionIndex: 1, IsSync: false},
		{Offset: 350, Size: 4, DecodeTime: 200, CompositionTime: 205, Duration: 100, DescriptionIndex: 1, IsSync: true},
	}
	for i, want := range wants {
		got, ok := table.Sample(i)
		if !ok || got != want {
			t.Errorf("sample %d = (%+v, %v), want %+v", i, got, ok, want)
		}
	}
	for i, want := range [][]byte{{1, 2, 3}, {4, 5}, {6, 7, 8, 9}} {
		got, readErr := table.Read(i)
		if readErr != nil || !bytes.Equal(got, want) {
			t.Errorf("Read(%d) = (%v, %v), want %v", i, got, readErr, want)
		}
	}
}

func TestApplyChunksRejectsSampleCountMismatch(t *testing.T) {
	samples := make([]Sample, 2)
	err := applyChunks(samples, []uint32{1, 1}, []uint64{100}, []sampleToChunk{{firstChunk: 1, samplesPerChunk: 1, descriptionIndex: 1}})
	if err == nil {
		t.Fatal("applyChunks accepted fewer mapped samples than sizes")
	}
}

func tableBox(name string, version byte, entries [][]uint32) []byte {
	entryWidth := len(entries[0])
	payload := make([]byte, 8+len(entries)*entryWidth*4)
	payload[0] = version
	binary.BigEndian.PutUint32(payload[4:], uint32(len(entries)))
	for i, entry := range entries {
		for j, value := range entry {
			binary.BigEndian.PutUint32(payload[8+(i*entryWidth+j)*4:], value)
		}
	}
	return box(name, payload)
}
