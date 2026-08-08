package h264

import (
	"encoding/binary"
	"testing"
)

func TestDecodeSingleMacroblockIDR(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, err := store.AddSPS(spsUnit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddPPS(ppsUnit); err != nil {
		t.Fatal(err)
	}
	// Keep the syntax fields from the shared baseline SPS while making this
	// test picture exactly one macroblock.
	sps.CodedWidth, sps.CodedHeight = 16, 16
	sps.Width, sps.Height = 16, 16
	store.sequences[sps.ID] = sps

	w := &testBitWriter{}
	w.ue(0) // first_mb_in_slice
	w.ue(2) // I slice
	w.ue(0) // PPS id
	w.bitsValue(0, 4)
	w.ue(0) // idr_pic_id
	w.bitsValue(0, 4)
	w.bit(0) // no_output_of_prior_pics_flag
	w.bit(0) // long_term_reference_flag
	w.se(0)  // slice_qp_delta
	w.ue(0)  // disable_deblocking_filter_idc
	w.se(0)
	w.se(0)
	w.ue(3)  // I16x16 DC, no AC, no chroma residual
	w.ue(0)  // chroma DC prediction
	w.se(0)  // mb_qp_delta
	w.bit(1) // zero-coefficient luma DC coeff_token
	w.rbspStop()
	unit, err := ParseNALUnit(append([]byte{0x65}, w.bytes()...))
	if err != nil {
		t.Fatal(err)
	}

	frame, err := DecodeIDRFrame([]NALUnit{unit}, store)
	if err != nil {
		t.Fatal(err)
	}
	for name, plane := range map[string][]uint8{"Y": frame.Y, "Cb": frame.Cb, "Cr": frame.Cr} {
		for i, value := range plane {
			if value != 128 {
				t.Fatalf("%s[%d] = %d, want 128", name, i, value)
			}
		}
	}
}

func TestDecodePCMMacroblock(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps

	w := &testBitWriter{}
	w.ue(0)
	w.ue(2)
	w.ue(0)
	w.bitsValue(0, 4)
	w.ue(0)
	w.bitsValue(0, 4)
	w.bit(0)
	w.bit(0)
	w.se(0)
	w.ue(0)
	w.se(0)
	w.se(0)
	w.ue(25) // I_PCM
	for w.bits%8 != 0 {
		w.bit(0)
	}
	for range 256 + 64 + 64 {
		w.bitsValue(128, 8)
	}
	w.rbspStop()
	unit, err := ParseNALUnit(append([]byte{0x65}, w.bytes()...))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodeIDRFrame([]NALUnit{unit}, store)
	if err != nil {
		t.Fatal(err)
	}
	for _, plane := range [][]uint8{frame.Y, frame.Cb, frame.Cr} {
		for _, value := range plane {
			if value != 128 {
				t.Fatalf("PCM value = %d, want 128", value)
			}
		}
	}
}

func TestDecodeWholePicturePSkip(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	reference, _ := NewFrame420(16, 16)
	for i := range reference.Y {
		reference.Y[i] = uint8(i)
	}
	for i := range reference.Cb {
		reference.Cb[i], reference.Cr[i] = uint8(i+40), uint8(i+80)
	}

	w := &testBitWriter{}
	w.ue(0) // first_mb_in_slice
	w.ue(0) // P slice
	w.ue(0) // PPS
	w.bitsValue(1, 4)
	w.bitsValue(2, 4)
	w.bit(0) // num_ref_idx_active_override_flag
	w.bit(0) // ref_pic_list_modification_flag_l0
	w.bit(0) // adaptive_ref_pic_marking_mode_flag
	w.se(0)
	w.ue(0)
	w.se(0)
	w.se(0)
	w.ue(1) // one P_Skip macroblock
	w.rbspStop()
	unit, err := ParseNALUnit(append([]byte{0x41}, w.bytes()...))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodePSkipFrame([]NALUnit{unit}, store, reference)
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2][]uint8{
		"Y": {frame.Y, reference.Y}, "Cb": {frame.Cb, reference.Cb}, "Cr": {frame.Cr, reference.Cr},
	} {
		for i := range pair[0] {
			if pair[0][i] != pair[1][i] {
				t.Fatalf("%s[%d] = %d, want %d", name, i, pair[0][i], pair[1][i])
			}
		}
	}
}

func TestDecodeP16x16IntegerMotion(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	reference, _ := NewFrame420(16, 16)
	for y := range 16 {
		for x := range 16 {
			reference.Y[y*16+x] = uint8(16 + x)
		}
	}
	for y := range 8 {
		for x := range 8 {
			reference.Cb[y*8+x], reference.Cr[y*8+x] = uint8(80+x*2), uint8(120+x*2)
		}
	}

	w := pSlicePrefix()
	w.ue(0) // mb_skip_run before a coded macroblock
	w.ue(0) // P_L0_16x16
	w.se(4) // one full luma pixel right
	w.se(0)
	w.ue(0) // coded_block_pattern = 0
	w.rbspStop()
	unit, err := ParseNALUnit(append([]byte{0x41}, w.bytes()...))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodePSkipFrame([]NALUnit{unit}, store, reference)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Y[0] != reference.Y[1] || frame.Y[15] != reference.Y[15] {
		t.Fatalf("integer luma prediction edge = (%d,%d)", frame.Y[0], frame.Y[15])
	}
	if frame.Cb[0] != 81 || frame.Cr[0] != 121 {
		t.Fatalf("fractional chroma prediction = (%d,%d), want (81,121)", frame.Cb[0], frame.Cr[0])
	}
}

func TestDecodeP16x8UsesIndependentPartitions(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	reference, _ := NewFrame420(16, 16)
	for y := range 16 {
		for x := range 16 {
			reference.Y[y*16+x] = uint8(y*16 + x)
		}
	}
	for i := range reference.Cb {
		reference.Cb[i], reference.Cr[i] = 128, 128
	}
	w := pSlicePrefix()
	w.ue(0)
	w.ue(1) // P_L0_L0_16x8
	w.se(4)
	w.se(0) // top partition MV = (4,0)
	w.se(0)
	w.se(4) // bottom predictor is top MV, result = (4,4)
	w.ue(0)
	w.rbspStop()
	unit, _ := ParseNALUnit(append([]byte{0x41}, w.bytes()...))
	frame, err := DecodePSkipFrame([]NALUnit{unit}, store, reference)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Y[0] != reference.Y[1] || frame.Y[8*16] != reference.Y[9*16+1] {
		t.Fatalf("partition samples = (%d,%d), want (%d,%d)", frame.Y[0], frame.Y[8*16], reference.Y[1], reference.Y[9*16+1])
	}
}

func TestDecodeP8x8UsesFourMotionVectors(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	reference, _ := NewFrame420(16, 16)
	for y := range 16 {
		for x := range 16 {
			reference.Y[y*16+x] = uint8(y*16 + x)
		}
	}
	for i := range reference.Cb {
		reference.Cb[i], reference.Cr[i] = 128, 128
	}
	w := pSlicePrefix()
	w.ue(0)
	w.ue(3) // P_8x8
	for range 4 {
		w.ue(0) // P_L0_8x8
	}
	for _, difference := range [][2]int64{{0, 0}, {4, 0}, {0, 4}, {4, 4}} {
		w.se(difference[0])
		w.se(difference[1])
	}
	w.ue(0)
	w.rbspStop()
	unit, _ := ParseNALUnit(append([]byte{0x41}, w.bytes()...))
	frame, err := DecodePSkipFrame([]NALUnit{unit}, store, reference)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct{ destination, source int }{
		{0, 0}, {8, 9}, {8 * 16, 9 * 16}, {8*16 + 8, 9*16 + 9},
	}
	for _, check := range checks {
		if frame.Y[check.destination] != reference.Y[check.source] {
			t.Fatalf("Y[%d] = %d, want reference[%d]=%d", check.destination, frame.Y[check.destination], check.source, reference.Y[check.source])
		}
	}
}

func TestDecodePFrameContainingIntra16Macroblock(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	reference, _ := NewFrame420(16, 16)
	for i := range reference.Y {
		reference.Y[i] = 32
	}
	for i := range reference.Cb {
		reference.Cb[i], reference.Cr[i] = 64, 192
	}
	w := pSlicePrefix()
	w.ue(0) // no skipped macroblocks
	w.ue(8) // P raw type 5 + I_16x16 DC type 3
	w.ue(0) // chroma DC mode
	w.se(0)
	w.bit(1) // zero luma DC coefficients
	w.rbspStop()
	unit, _ := ParseNALUnit(append([]byte{0x41}, w.bytes()...))
	frame, err := DecodePSkipFrame([]NALUnit{unit}, store, reference)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Y[0] != 128 || frame.Cb[0] != 128 || frame.Cr[0] != 128 {
		t.Fatalf("P-picture intra samples = (%d,%d,%d)", frame.Y[0], frame.Cb[0], frame.Cr[0])
	}
}

func TestDecodePPartitionSelectsSecondReference(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	pps, _ := store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	pps.DefaultReferenceL0 = 2
	store.pictures[pps.ID] = pps
	w := pSlicePrefix()
	w.ue(0)
	w.ue(0) // P_L0_16x16
	w.ue(1) // ref_idx_l0
	w.se(0)
	w.se(0)
	w.ue(0)
	w.rbspStop()
	unit, _ := ParseNALUnit(append([]byte{0x41}, w.bytes()...))
	frame, err := DecodePFrame([]NALUnit{unit}, store, []*Frame420{solidReference(40), solidReference(180)})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Y[0] != 180 {
		t.Fatalf("second reference prediction = %d, want 180", frame.Y[0])
	}
}

func TestDecodeB16x16L0L1AndBiPrediction(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	references := [2][]*Frame420{{solidReference(40)}, {solidReference(200)}}
	for _, test := range []struct {
		raw  uint64
		want uint8
	}{{1, 40}, {2, 200}, {3, 120}} {
		w := bSlicePrefix()
		w.ue(0) // no B_Skip macroblocks
		w.ue(test.raw)
		if test.raw == 1 || test.raw == 3 {
			w.se(0)
			w.se(0)
		}
		if test.raw == 2 || test.raw == 3 {
			w.se(0)
			w.se(0)
		}
		w.ue(0)
		w.rbspStop()
		unit, _ := ParseNALUnit(append([]byte{0x01}, w.bytes()...))
		frame, err := DecodeBFrame([]NALUnit{unit}, store, references)
		if err != nil {
			t.Fatalf("raw %d: %v", test.raw, err)
		}
		if frame.Y[0] != test.want {
			t.Fatalf("raw %d prediction = %d, want %d", test.raw, frame.Y[0], test.want)
		}
	}
}

func TestDecodeBSplitL0L1Prediction(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	w := bSlicePrefix()
	w.ue(0)
	w.ue(8) // B_L0_L1_16x8
	w.se(0)
	w.se(0) // top list 0 MVD
	w.se(0)
	w.se(0) // bottom list 1 MVD
	w.ue(0)
	w.rbspStop()
	unit, _ := ParseNALUnit(append([]byte{0x01}, w.bytes()...))
	frame, err := DecodeBFrame([]NALUnit{unit}, store, [2][]*Frame420{{solidReference(30)}, {solidReference(210)}})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Y[0] != 30 || frame.Y[8*16] != 210 {
		t.Fatalf("split B prediction = (%d,%d)", frame.Y[0], frame.Y[8*16])
	}
}

func TestDecodeB8x8SubMacroblocks(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	w := bSlicePrefix()
	w.ue(0)
	w.ue(22) // B_8x8
	for range 4 {
		w.ue(1) // B_L0_8x8
	}
	for range 4 {
		w.se(0)
		w.se(0)
	}
	w.ue(0)
	w.rbspStop()
	unit, _ := ParseNALUnit(append([]byte{0x01}, w.bytes()...))
	frame, err := DecodeBFrame([]NALUnit{unit}, store, [2][]*Frame420{{solidReference(70)}, {solidReference(190)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{0, 8, 8 * 16, 8*16 + 8} {
		if frame.Y[index] != 70 {
			t.Fatalf("B_8x8 Y[%d] = %d, want 70", index, frame.Y[index])
		}
	}
}

func TestTemporalDirectMotionScaling(t *testing.T) {
	past, future := solidReference(40), solidReference(200)
	past.poc, future.poc = 0, 4
	future.motion[0] = make(map[[2]int]motionInfo)
	for y := range 4 {
		for x := range 4 {
			future.motion[0][[2]int{x, y}] = motionInfo{vector: [2]int{8, 0}, reference: 0, picture: past}
		}
	}
	header := SliceHeader{PictureOrderCountLSB: 2}
	partition, err := temporalDirectPartition(
		resolvedBPartition{InterPartition: InterPartition{Width: 4, Height: 4, Direct: true}},
		header, [2][]*Frame420{{past}, {future}}, 0, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if partition.MotionL0 != [2]int{4, 0} || partition.MotionL1 != [2]int{-4, 0} {
		t.Fatalf("temporal direct vectors = (%v,%v)", partition.MotionL0, partition.MotionL1)
	}
}

func TestDecodeBSkipSpatialDirect(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	w := bSlicePrefix()
	w.ue(1) // one B_Skip macroblock
	w.rbspStop()
	unit, _ := ParseNALUnit(append([]byte{0x01}, w.bytes()...))
	frame, err := DecodeBFrame([]NALUnit{unit}, store, [2][]*Frame420{{solidReference(40)}, {solidReference(200)}})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Y[0] != 120 {
		t.Fatalf("B_Skip bidirectional prediction = %d, want 120", frame.Y[0])
	}
}

func bSlicePrefix() *testBitWriter {
	w := &testBitWriter{}
	w.ue(0)
	w.ue(1) // B slice
	w.ue(0)
	w.bitsValue(1, 4)
	w.bitsValue(2, 4)
	w.bit(1) // direct_spatial_mv_pred_flag
	w.bit(0) // reference count override
	w.bit(0) // list 0 modification
	w.bit(0) // list 1 modification
	w.se(0)
	w.ue(1) // disable deblocking
	return w
}

func pSlicePrefix() *testBitWriter {
	w := &testBitWriter{}
	w.ue(0)
	w.ue(0)
	w.ue(0)
	w.bitsValue(1, 4)
	w.bitsValue(2, 4)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.se(0)
	w.ue(1) // disable deblocking; motion tests verify prediction samples exactly
	return w
}

func TestLumaQuarterPelSixTapInterpolation(t *testing.T) {
	frame, _ := NewFrame420(16, 16)
	for y := range 16 {
		for x := 8; x < 16; x++ {
			frame.Y[y*16+x] = 255
		}
	}
	if got := lumaQuarterSample(frame, 7, 7, 2, 0); got != 128 {
		t.Fatalf("half-pel = %d, want 128", got)
	}
	if got := lumaQuarterSample(frame, 7, 7, 1, 0); got != 64 {
		t.Fatalf("left quarter-pel = %d, want 64", got)
	}
	if got := lumaQuarterSample(frame, 7, 7, 3, 0); got != 192 {
		t.Fatalf("right quarter-pel = %d, want 192", got)
	}
	if got := lumaQuarterSample(frame, 7, 7, 2, 2); got != 128 {
		t.Fatalf("diagonal half-pel = %d, want 128", got)
	}
}

func TestWeightedPPartitionPrediction(t *testing.T) {
	var luma [256]uint8
	var chroma [2][64]uint8
	for i := range luma {
		luma[i] = 100
	}
	for component := range 2 {
		for i := range chroma[component] {
			chroma[component][i] = 80
		}
	}
	slice := SliceHeader{
		LumaLog2WeightDenom: 1, ChromaLog2WeightDenom: 2,
		PredictionWeights: [2][]PredictionWeight{{{
			LumaWeight: 3, LumaOffset: -10,
			ChromaWeight: [2]int64{2, 6}, ChromaOffset: [2]int64{5, -5},
		}}, nil},
	}
	partition := resolvedInterPartition{InterPartition: InterPartition{X: 8, Y: 8, Width: 8, Height: 8}}
	applyWeightedPartition(&luma, &chroma, partition, slice)
	if luma[8*16+8] != 140 || luma[0] != 100 {
		t.Fatalf("weighted luma = (%d outside %d)", luma[8*16+8], luma[0])
	}
	if chroma[0][4*8+4] != 45 || chroma[1][4*8+4] != 115 || chroma[0][0] != 80 {
		t.Fatalf("weighted chroma = (%d,%d outside %d)", chroma[0][4*8+4], chroma[1][4*8+4], chroma[0][0])
	}
}

func TestExplicitWeightedBPrediction(t *testing.T) {
	if got := weightedBPrediction(100, 200, true, true, sampleWeight{2, 10}, sampleWeight{2, -10}, 1); got != 150 {
		t.Fatalf("weighted biprediction = %d, want 150", got)
	}
	if got := weightedBPrediction(100, 0, true, false, sampleWeight{3, -10}, sampleWeight{}, 1); got != 140 {
		t.Fatalf("weighted list-0 prediction = %d, want 140", got)
	}
}

func TestImplicitWeightedBPrediction(t *testing.T) {
	past, future := solidReference(40), solidReference(200)
	past.poc, future.poc = 0, 4
	slice := SliceHeader{PictureOrderCountLSB: 1}
	if got := implicitBPrediction(40, 200, slice, past, future); got != 80 {
		t.Fatalf("implicit weighted prediction = %d, want 80", got)
	}
	past.longTerm = true
	if got := implicitBPrediction(40, 200, slice, past, future); got != 120 {
		t.Fatalf("long-term implicit prediction = %d, want 120", got)
	}
}

func TestDecoderKeepsReferenceAcrossMP4Samples(t *testing.T) {
	decoder, err := NewDecoder(AVCConfig{
		NALLengthSize: 4, SequenceHeaders: [][]byte{baselineSPS()}, PictureHeaders: [][]byte{baselinePPS()},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The shared SPS is 640x480, so construct a complete all-PCM IDR picture.
	// This deliberately exercises state through the public MP4-sample API.
	w := &testBitWriter{}
	w.ue(0)
	w.ue(2)
	w.ue(0)
	w.bitsValue(0, 4)
	w.ue(0)
	w.bitsValue(0, 4)
	w.bit(0)
	w.bit(0)
	w.se(0)
	w.ue(1) // disable deblocking for this large synthetic picture
	for range 40 * 30 {
		w.ue(25)
		for w.bits%8 != 0 {
			w.bit(0)
		}
		for range 384 {
			w.bitsValue(128, 8)
		}
	}
	w.rbspStop()
	idr := append([]byte{0x65}, w.bytes()...)
	first, err := decoder.DecodeSample(lengthPrefixedNAL(idr))
	if err != nil {
		t.Fatal(err)
	}
	if first.Y[0] != 128 {
		t.Fatalf("IDR first sample = %d", first.Y[0])
	}

	w = pSlicePrefix()
	w.ue(40 * 30)
	w.rbspStop()
	p := append([]byte{0x41}, w.bytes()...)
	second, err := decoder.DecodeSample(lengthPrefixedNAL(p))
	if err != nil {
		t.Fatal(err)
	}
	if second.Y[0] != 128 || second.Y[len(second.Y)-1] != 128 {
		t.Fatalf("P reference copy endpoints = (%d,%d)", second.Y[0], second.Y[len(second.Y)-1])
	}
}

func lengthPrefixedNAL(nal []byte) []byte {
	result := make([]byte, 4, len(nal)+4)
	binary.BigEndian.PutUint32(result, uint32(len(nal)))
	return append(result, nal...)
}
