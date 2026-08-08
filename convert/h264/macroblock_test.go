package h264

import (
	"errors"
	"testing"
)

func TestParseIntra4x4Macroblock(t *testing.T) {
	w := &testBitWriter{}
	w.ue(0) // I_NxN
	for range 16 {
		w.bit(1) // use predicted Intra4x4 mode
	}
	w.ue(0) // intra_chroma_pred_mode
	w.ue(3) // Intra CBP codeNum 3 maps to CBP 0
	w.rbspStop()
	r := readerFromBits(bitString(w.data, w.bits))
	header, err := ParseMacroblockHeader(r, baselineSliceHeader(SliceI))
	if err != nil {
		t.Fatal(err)
	}
	if header.Kind != MacroblockIntra4x4 || header.CodedBlockPatternLuma != 0 || header.CodedBlockPatternChroma != 0 {
		t.Fatalf("macroblock = %+v", header)
	}
	for block, mode := range header.Intra4x4Prediction {
		if !mode.Previous {
			t.Errorf("block %d did not retain predicted-mode flag", block)
		}
	}
}

func TestParseIntra16x16Macroblock(t *testing.T) {
	w := &testBitWriter{}
	w.ue(21) // prediction mode 0, luma CBP 15, chroma CBP 2
	w.ue(2)  // intra_chroma_pred_mode
	w.se(-1) // mb_qp_delta
	w.rbspStop()
	header, err := ParseMacroblockHeader(readerFromBits(bitString(w.data, w.bits)), baselineSliceHeader(SliceI))
	if err != nil {
		t.Fatal(err)
	}
	if header.Kind != MacroblockIntra16x16 || header.Intra16x16Prediction != 0 ||
		header.CodedBlockPatternLuma != 15 || header.CodedBlockPatternChroma != 2 || header.QPDelta != -1 {
		t.Fatalf("macroblock = %+v", header)
	}
}

func TestInterErrorAndPCMClassification(t *testing.T) {
	w := &testBitWriter{}
	w.ue(0)
	if _, err := ParseMacroblockHeader(readerFromBits(bitString(w.data, w.bits)), baselineSliceHeader(SliceSI)); !errors.Is(err, ErrInterMacroblockUnsupported) {
		t.Fatalf("inter error = %v", err)
	}
	w = &testBitWriter{}
	w.ue(25)
	header, err := ParseMacroblockHeader(readerFromBits(bitString(w.data, w.bits)), baselineSliceHeader(SliceI))
	if err != nil || header.Kind != MacroblockPCM {
		t.Fatalf("PCM header = (%+v, %v)", header, err)
	}
}

func TestParseP16x16Macroblock(t *testing.T) {
	w := &testBitWriter{}
	w.ue(0)
	w.se(4)
	w.se(-2)
	w.ue(1) // inter CBP 16: chroma DC only
	w.se(1)
	header, err := ParseMacroblockHeader(readerFromBits(bitString(w.data, w.bits)), baselineSliceHeader(SliceP))
	if err != nil {
		t.Fatal(err)
	}
	if header.Kind != MacroblockInter || header.MotionVectorDifference != [2]int64{4, -2} ||
		header.CodedBlockPatternLuma != 0 || header.CodedBlockPatternChroma != 1 || header.QPDelta != 1 {
		t.Fatalf("P_L0_16x16 header = %+v", header)
	}
}

func TestParseP16x8AndP8x16Partitions(t *testing.T) {
	for _, test := range []struct {
		raw             uint64
		width0, height0 int
		x1, y1          int
	}{
		{raw: 1, width0: 16, height0: 8, y1: 8},
		{raw: 2, width0: 8, height0: 16, x1: 8},
	} {
		w := &testBitWriter{}
		w.ue(test.raw)
		w.se(1)
		w.se(2)
		w.se(3)
		w.se(4)
		w.ue(0)
		header, err := ParseMacroblockHeader(readerFromBits(bitString(w.data, w.bits)), baselineSliceHeader(SliceP))
		if err != nil {
			t.Fatal(err)
		}
		if len(header.InterPartitions) != 2 || header.InterPartitions[0].Width != test.width0 ||
			header.InterPartitions[0].Height != test.height0 || header.InterPartitions[1].X != test.x1 ||
			header.InterPartitions[1].Y != test.y1 || header.InterPartitions[1].MotionDifference != [2]int64{3, 4} {
			t.Fatalf("raw %d partitions = %+v", test.raw, header.InterPartitions)
		}
	}
}

func TestParseP8x8SubPartitions(t *testing.T) {
	w := &testBitWriter{}
	w.ue(3)
	w.ue(0) // 8x8
	w.ue(1) // two 8x4
	w.ue(2) // two 4x8
	w.ue(3) // four 4x4
	for range 1 + 2 + 2 + 4 {
		w.se(0)
		w.se(0)
	}
	w.ue(0)
	header, err := ParseMacroblockHeader(readerFromBits(bitString(w.data, w.bits)), baselineSliceHeader(SliceP))
	if err != nil {
		t.Fatal(err)
	}
	if len(header.InterPartitions) != 9 || header.InterPartitions[0].Width != 8 ||
		header.InterPartitions[1].Height != 4 || header.InterPartitions[3].Width != 4 ||
		header.InterPartitions[5].Width != 4 || header.InterPartitions[5].Height != 4 {
		t.Fatalf("P_8x8 partitions = %+v", header.InterPartitions)
	}
}

func TestParseB16x16PredictionModes(t *testing.T) {
	for _, raw := range []uint64{1, 2, 3} {
		w := &testBitWriter{}
		w.ue(raw)
		if raw == 1 || raw == 3 {
			w.se(1)
			w.se(2)
		}
		if raw == 2 || raw == 3 {
			w.se(3)
			w.se(4)
		}
		w.ue(0)
		header := baselineSliceHeader(SliceB)
		header.ReferenceCount = [2]uint64{1, 1}
		parsed, err := ParseMacroblockHeader(readerFromBits(bitString(w.data, w.bits)), header)
		if err != nil {
			t.Fatal(err)
		}
		partition := parsed.InterPartitions[0]
		if partition.UseList0 != (raw == 1 || raw == 3) || partition.UseList1 != (raw == 2 || raw == 3) {
			t.Fatalf("B raw %d partition = %+v", raw, partition)
		}
	}
}

func baselineSliceHeader(sliceType SliceType) SliceHeader {
	return SliceHeader{
		Type: sliceType,
		SPS:  SPS{ChromaFormat: 1, BitDepthLuma: 8, FrameMbsOnly: true},
		PPS:  PPS{InitialQP: 26},
	}
}

func bitString(data []byte, bitCount int) string {
	result := make([]byte, bitCount)
	for i := range result {
		result[i] = '0' + (data[i/8] >> (7 - i%8) & 1)
	}
	return string(result)
}
