package video

import (
	"bytes"
	"encoding/binary"
	"testing"

	"conb"
)

func TestMP4ToCanvasEndToEnd(t *testing.T) {
	sps, pps := testSPS(), testPPS()
	config := []byte{1, 66, 0, 30, 0xff, 0xe1, 0, byte(len(sps))}
	config = append(config, sps...)
	config = append(config, 1, 0, byte(len(pps)))
	config = append(config, pps...)
	idr := testPCMIDR()
	sample := make([]byte, 4, len(idr)+4)
	binary.BigEndian.PutUint32(sample, uint32(len(idr)))
	sample = append(sample, idr...)
	data := testMP4(config, sample)

	stream, err := OpenMP4(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if stream.Frames() != 1 {
		t.Fatalf("frame count = %d, want 1", stream.Frames())
	}
	order := stream.PresentationOrder()
	if len(order) != 1 || order[0] != 0 {
		t.Fatalf("presentation order = %v, want [0]", order)
	}
	duration, err := stream.PresentationDuration(order, 0)
	if err != nil || duration != 40_000_000 {
		t.Fatalf("presentation duration = %v, %v", duration, err)
	}
	frame, err := stream.Decode(0)
	if err != nil {
		t.Fatal(err)
	}
	canvas, _ := conb.NewCanvas(16, 16)
	if err = frame.DrawCanvas(canvas); err != nil {
		t.Fatal(err)
	}
	pixel, _ := canvas.Pixel(0, 0)
	if pixel.R < 130 || pixel.R > 131 || pixel.G != pixel.R || pixel.B != pixel.R || pixel.A != 255 {
		t.Fatalf("decoded canvas pixel = %+v", pixel)
	}
}

func testMP4(config, sample []byte) []byte {
	mdat := testBox("mdat", sample)
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:], 1000)
	binary.BigEndian.PutUint32(mvhd[16:], 40)
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[12:], 1)
	binary.BigEndian.PutUint32(tkhd[76:], 16<<16)
	binary.BigEndian.PutUint32(tkhd[80:], 16<<16)
	mdhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mdhd[12:], 1000)
	binary.BigEndian.PutUint32(mdhd[16:], 40)
	hdlr := make([]byte, 12)
	copy(hdlr[8:], "vide")
	visual := make([]byte, 78)
	binary.BigEndian.PutUint16(visual[6:], 1)
	binary.BigEndian.PutUint16(visual[24:], 16)
	binary.BigEndian.PutUint16(visual[26:], 16)
	visual = append(visual, testBox("avcC", config)...)
	stsd := make([]byte, 8)
	binary.BigEndian.PutUint32(stsd[4:], 1)
	stsd = append(stsd, testBox("avc1", visual)...)
	stts := testTable("stts", []uint32{1, 40})
	stsc := testTable("stsc", []uint32{1, 1, 1})
	stsz := make([]byte, 16)
	binary.BigEndian.PutUint32(stsz[8:], 1)
	binary.BigEndian.PutUint32(stsz[12:], uint32(len(sample)))
	stco := testTable("stco", []uint32{8})
	stbl := testBox("stbl", append(append(append(append(testBox("stsd", stsd), stts...), stsc...), testBox("stsz", stsz)...), stco...))
	mdia := testBox("mdia", append(append(testBox("mdhd", mdhd), testBox("hdlr", hdlr)...), testBox("minf", stbl)...))
	trak := testBox("trak", append(testBox("tkhd", tkhd), mdia...))
	moov := testBox("moov", append(testBox("mvhd", mvhd), trak...))
	return append(mdat, moov...)
}

func testBox(kind string, payload []byte) []byte {
	result := make([]byte, 8, len(payload)+8)
	binary.BigEndian.PutUint32(result, uint32(len(payload)+8))
	copy(result[4:], kind)
	return append(result, payload...)
}

func testTable(kind string, values []uint32) []byte {
	payload := make([]byte, 8+len(values)*4)
	binary.BigEndian.PutUint32(payload[4:], 1)
	for i, value := range values {
		binary.BigEndian.PutUint32(payload[8+i*4:], value)
	}
	return testBox(kind, payload)
}

type videoBitWriter struct {
	data []byte
	bits int
}

func (w *videoBitWriter) bit(value uint8) {
	if w.bits%8 == 0 {
		w.data = append(w.data, 0)
	}
	if value != 0 {
		w.data[len(w.data)-1] |= 1 << (7 - w.bits%8)
	}
	w.bits++
}
func (w *videoBitWriter) fixed(value uint64, count int) {
	for i := count - 1; i >= 0; i-- {
		w.bit(uint8(value >> i & 1))
	}
}
func (w *videoBitWriter) ue(value uint64) {
	code, count := value+1, 0
	for n := code; n != 0; n >>= 1 {
		count++
	}
	for range count - 1 {
		w.bit(0)
	}
	w.fixed(code, count)
}
func (w *videoBitWriter) se(value int64) {
	if value > 0 {
		w.ue(uint64(value*2 - 1))
	} else {
		w.ue(uint64(-value * 2))
	}
}
func (w *videoBitWriter) stop() {
	w.bit(1)
	for w.bits%8 != 0 {
		w.bit(0)
	}
}

func testSPS() []byte {
	w := &videoBitWriter{}
	w.fixed(66, 8)
	w.fixed(0, 8)
	w.fixed(30, 8)
	w.ue(0)
	w.ue(0)
	w.ue(0)
	w.ue(0)
	w.ue(1)
	w.bit(0)
	w.ue(0)
	w.ue(0)
	w.bit(1)
	w.bit(1)
	w.bit(0)
	w.bit(0)
	w.stop()
	return append([]byte{0x67}, w.data...)
}

func testPPS() []byte {
	w := &videoBitWriter{}
	w.ue(0)
	w.ue(0)
	w.bit(0)
	w.bit(0)
	w.ue(0)
	w.ue(0)
	w.ue(0)
	w.bit(0)
	w.fixed(0, 2)
	w.se(0)
	w.se(0)
	w.se(0)
	w.bit(1)
	w.bit(0)
	w.bit(0)
	w.stop()
	return append([]byte{0x68}, w.data...)
}

func testPCMIDR() []byte {
	w := &videoBitWriter{}
	w.ue(0)
	w.ue(2)
	w.ue(0)
	w.fixed(0, 4)
	w.ue(0)
	w.fixed(0, 4)
	w.bit(0)
	w.bit(0)
	w.se(0)
	w.ue(1)
	w.ue(25)
	for w.bits%8 != 0 {
		w.bit(0)
	}
	for range 384 {
		w.fixed(128, 8)
	}
	w.stop()
	return append([]byte{0x65}, w.data...)
}
