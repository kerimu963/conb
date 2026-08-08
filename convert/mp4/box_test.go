package mp4

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestParseBoxesAndFileType(t *testing.T) {
	data := box("ftyp", append(append([]byte("isom"), 0, 0, 2, 0), []byte("isommp42")...))
	data = append(data, box("mdat", []byte{1, 2, 3, 4})...)
	file, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Parse returned an error: %v", err)
	}
	if len(file.Boxes()) != 2 {
		t.Fatalf("box count = %d, want 2", len(file.Boxes()))
	}
	fileType, err := file.FileType()
	if err != nil {
		t.Fatalf("FileType returned an error: %v", err)
	}
	if fileType.MajorBrand != Type("isom") || fileType.MinorVersion != 512 {
		t.Fatalf("file type = %+v", fileType)
	}
	if len(fileType.CompatibleBrands) != 2 || fileType.CompatibleBrands[1] != Type("mp42") {
		t.Fatalf("compatible brands = %v", fileType.CompatibleBrands)
	}
}

func TestLargeAndToEndBoxes(t *testing.T) {
	large := make([]byte, 20)
	binary.BigEndian.PutUint32(large[:4], 1)
	copy(large[4:8], "free")
	binary.BigEndian.PutUint64(large[8:16], 20)
	file, err := Parse(bytes.NewReader(large), int64(len(large)))
	if err != nil || file.Boxes()[0].HeaderSize != 16 {
		t.Fatalf("large box parse = (%v, %v)", file, err)
	}

	toEnd := box("mdat", []byte{1, 2, 3})
	for i := range 4 {
		toEnd[i] = 0
	}
	file, err = Parse(bytes.NewReader(toEnd), int64(len(toEnd)))
	if err != nil || file.Boxes()[0].Size != uint64(len(toEnd)) {
		t.Fatalf("to-end box parse = (%v, %v)", file, err)
	}
}

func TestParseRejectsMalformedBoxes(t *testing.T) {
	tests := [][]byte{
		{0, 0, 0, 4, 'f', 'r', 'e', 'e'},
		{0, 0, 0, 16, 'f', 'r', 'e', 'e'},
		{0, 0, 0, 1, 'f', 'r', 'e', 'e'},
		{1, 2, 3},
	}
	for _, data := range tests {
		if _, err := Parse(bytes.NewReader(data), int64(len(data))); !errors.Is(err, ErrMalformed) {
			t.Errorf("Parse(%v) error = %v, want ErrMalformed", data, err)
		}
	}
}

func TestChildren(t *testing.T) {
	child := box("free", nil)
	data := box("moov", child)
	file, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	children, err := file.Boxes()[0].Children()
	if err != nil || len(children) != 1 || children[0].Type != Type("free") {
		t.Fatalf("Children = (%v, %v)", children, err)
	}
}

func box(name string, payload []byte) []byte {
	result := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(result[:4], uint32(len(result)))
	copy(result[4:8], name)
	copy(result[8:], payload)
	return result
}
