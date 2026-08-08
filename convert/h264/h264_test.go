package h264

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestParseAVCConfigAndSample(t *testing.T) {
	sps := baselineSPS()
	pps := baselinePPS()
	configData := []byte{1, 66, 0, 30, 0xff, 0xe1}
	configData = appendLength16(configData, sps)
	configData = append(configData, 1)
	configData = appendLength16(configData, pps)

	config, err := ParseAVCConfig(configData)
	if err != nil {
		t.Fatal(err)
	}
	if config.Profile != 66 || config.Level != 30 || config.NALLengthSize != 4 ||
		len(config.SequenceHeaders) != 1 || len(config.PictureHeaders) != 1 {
		t.Fatalf("config = %+v", config)
	}

	sampleData := make([]byte, 4, 4+len(sps)+4+len(pps))
	binary.BigEndian.PutUint32(sampleData, uint32(len(sps)))
	sampleData = append(sampleData, sps...)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(pps)))
	sampleData = append(sampleData, length...)
	sampleData = append(sampleData, pps...)
	units, err := ParseSample(sampleData, config.NALLengthSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 || units[0].Type != NALSPS || units[1].Type != NALPPS {
		t.Fatalf("NAL units = %+v", units)
	}

	annexB := AnnexB(units)
	parsed, err := ParseAnnexB(annexB)
	if err != nil || len(parsed) != 2 || !bytes.Equal(parsed[0].Data, sps) {
		t.Fatalf("ParseAnnexB = (%v, %v)", parsed, err)
	}
}

func TestParseParameterSets(t *testing.T) {
	spsUnit, _ := newNALUnit(baselineSPS())
	sps, err := ParseSPS(spsUnit)
	if err != nil {
		t.Fatal(err)
	}
	if sps.ID != 0 || sps.Profile != 66 || sps.Level != 30 || sps.Width != 640 || sps.Height != 480 {
		t.Fatalf("SPS = %+v", sps)
	}
	ppsUnit, _ := newNALUnit(baselinePPS())
	pps, err := ParsePPS(ppsUnit)
	if err != nil {
		t.Fatal(err)
	}
	if pps.ID != 0 || pps.SequenceID != 0 || pps.EntropyCodingCABAC || pps.InitialQP != 26 || pps.DefaultReferenceL0 != 1 {
		t.Fatalf("PPS = %+v", pps)
	}
}

func TestParameterStoreAndSliceHeaders(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	if _, err := store.AddSPS(spsUnit); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddPPS(ppsUnit); err != nil {
		t.Fatal(err)
	}

	idr := sliceNAL(0x65, SliceI, 0, 0, 0)
	header, err := ParseSliceHeader(idr, store)
	if err != nil {
		t.Fatal(err)
	}
	if header.Type != SliceI || header.FrameNumber != 0 || header.IDRPictureID != 0 || header.PictureOrderCountLSB != 0 {
		t.Fatalf("IDR slice header = %+v", header)
	}
	reader, err := SliceDataReader(idr, header)
	if err != nil || reader.BitsRemaining() == 0 {
		t.Fatalf("SliceDataReader = (%v, %v)", reader, err)
	}

	pSlice := sliceNAL(0x41, SliceP, 1, 2, 0)
	header, err = ParseSliceHeader(pSlice, store)
	if err != nil {
		t.Fatal(err)
	}
	if header.Type != SliceP || header.FrameNumber != 1 || header.PictureOrderCountLSB != 2 {
		t.Fatalf("P slice header = %+v", header)
	}
}

func TestSliceHeaderRequiresKnownPPS(t *testing.T) {
	unit := sliceNAL(0x41, SliceP, 0, 0, 0)
	if _, err := ParseSliceHeader(unit, NewParameterSetStore()); !errors.Is(err, ErrMalformed) {
		t.Fatalf("missing PPS error = %v", err)
	}
}

func TestSliceHeaderPredictionReferenceAndQP(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	store.AddSPS(spsUnit)
	store.AddPPS(ppsUnit)
	pps := store.pictures[0]
	pps.WeightedPrediction = true
	pps.EntropyCodingCABAC = true
	store.pictures[0] = pps

	w := &testBitWriter{}
	w.ue(0) // first_mb_in_slice
	w.ue(uint64(SliceP))
	w.ue(0)           // PPS
	w.bitsValue(3, 4) // frame_num
	w.bitsValue(6, 4) // pic_order_cnt_lsb
	w.bit(0)          // reference count override
	w.bit(1)          // modify list 0
	w.ue(0)           // subtract short-term picture number
	w.ue(2)           // abs_diff_pic_num_minus1
	w.ue(3)           // end modifications
	w.ue(0)           // luma_log2_weight_denom
	w.ue(0)           // chroma_log2_weight_denom
	w.bit(1)          // luma_weight_l0_flag
	w.se(1)
	w.se(-1)
	w.bit(0) // chroma_weight_l0_flag
	w.bit(1) // adaptive reference marking
	w.ue(1)  // MMCO short-term unused
	w.ue(0)
	w.ue(0)  // end MMCO
	w.ue(2)  // cabac_init_idc
	w.se(-2) // slice_qp_delta
	w.ue(2)  // disable_deblocking_filter_idc
	w.se(1)
	w.se(-1)
	w.rbspStop()
	unit, _ := ParseNALUnit(append([]byte{0x41}, w.bytes()...))
	header, err := ParseSliceHeader(unit, store)
	if err != nil {
		t.Fatal(err)
	}
	if header.SliceQP != 24 || header.CABACInitIDC != 2 || len(header.ReferenceModifications) != 1 ||
		len(header.MemoryManagement) != 1 || len(header.PredictionWeights[0]) != 1 ||
		header.PredictionWeights[0][0].LumaOffset != -1 || header.SliceAlphaOffset != 2 || header.SliceBetaOffset != -2 {
		t.Fatalf("extended slice header = %+v", header)
	}
}

func TestBitReaderExpGolombAndEmulationPrevention(t *testing.T) {
	r, err := NewBitReader([]byte{0, 0, 3, 1, 0x80})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := r.ReadBits(24)
	if first != 1 {
		t.Fatalf("unescaped first 24 bits = %#x, want 1", first)
	}

	w := &testBitWriter{}
	w.ue(0)
	w.ue(5)
	w.se(-3)
	r, _ = NewBitReader(w.bytes())
	if got, _ := r.ReadUE(); got != 0 {
		t.Fatalf("first UE = %d", got)
	}
	if got, _ := r.ReadUE(); got != 5 {
		t.Fatalf("second UE = %d", got)
	}
	if got, _ := r.ReadSE(); got != -3 {
		t.Fatalf("SE = %d", got)
	}
}

func TestRejectsMalformedNALData(t *testing.T) {
	if _, err := ParseSample([]byte{0, 0, 0, 4, 0x67}, 4); !errors.Is(err, ErrMalformed) {
		t.Fatalf("truncated sample error = %v", err)
	}
	if _, err := ParseAnnexB([]byte{1, 2, 3}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("missing start code error = %v", err)
	}
	if _, err := newNALUnit([]byte{0x80}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("forbidden bit error = %v", err)
	}
}

func baselineSPS() []byte {
	w := &testBitWriter{}
	w.bitsValue(66, 8)
	w.bitsValue(0, 8)
	w.bitsValue(30, 8)
	w.ue(0)  // seq_parameter_set_id
	w.ue(0)  // log2_max_frame_num_minus4
	w.ue(0)  // pic_order_cnt_type
	w.ue(0)  // log2_max_pic_order_cnt_lsb_minus4
	w.ue(1)  // max_num_ref_frames
	w.bit(0) // gaps_in_frame_num_value_allowed_flag
	w.ue(39) // 40 macroblocks = 640
	w.ue(29) // 30 macroblocks = 480
	w.bit(1) // frame_mbs_only_flag
	w.bit(1) // direct_8x8_inference_flag
	w.bit(0) // frame_cropping_flag
	w.bit(0) // vui_parameters_present_flag
	w.rbspStop()
	return append([]byte{0x67}, w.bytes()...)
}

func baselinePPS() []byte {
	w := &testBitWriter{}
	w.ue(0)
	w.ue(0)
	w.bit(0)
	w.bit(0)
	w.ue(0)  // num_slice_groups_minus1
	w.ue(0)  // num_ref_idx_l0_default_active_minus1
	w.ue(0)  // num_ref_idx_l1_default_active_minus1
	w.bit(0) // weighted_pred_flag
	w.bitsValue(0, 2)
	w.se(0)  // pic_init_qp_minus26
	w.se(0)  // pic_init_qs_minus26
	w.se(0)  // chroma_qp_index_offset
	w.bit(1) // deblocking_filter_control_present_flag
	w.bit(0) // constrained_intra_pred_flag
	w.bit(0) // redundant_pic_cnt_present_flag
	w.rbspStop()
	return append([]byte{0x68}, w.bytes()...)
}

func sliceNAL(headerByte byte, sliceType SliceType, frameNumber, pocLSB, idrID uint64) NALUnit {
	w := &testBitWriter{}
	w.ue(0)
	w.ue(uint64(sliceType))
	w.ue(0)
	w.bitsValue(frameNumber, 4)
	if NALType(headerByte&0x1f) == NALSliceIDR {
		w.ue(idrID)
	}
	w.bitsValue(pocLSB, 4)
	if sliceType == SliceB {
		w.bit(1) // direct_spatial_mv_pred_flag
	}
	if sliceType == SliceP || sliceType == SliceSP || sliceType == SliceB {
		w.bit(0) // num_ref_idx_active_override_flag
	}
	if sliceType != SliceI && sliceType != SliceSI {
		w.bit(0) // ref_pic_list_modification_flag_l0
		if sliceType == SliceB {
			w.bit(0) // ref_pic_list_modification_flag_l1
		}
	}
	if headerByte>>5&3 != 0 {
		if NALType(headerByte&0x1f) == NALSliceIDR {
			w.bit(0) // no_output_of_prior_pics_flag
			w.bit(0) // long_term_reference_flag
		} else {
			w.bit(0) // adaptive_ref_pic_marking_mode_flag
		}
	}
	w.se(0) // slice_qp_delta
	w.ue(0) // disable_deblocking_filter_idc
	w.se(0) // slice_alpha_c0_offset_div2
	w.se(0) // slice_beta_offset_div2
	w.rbspStop()
	unit, _ := ParseNALUnit(append([]byte{headerByte}, w.bytes()...))
	return unit
}

func appendLength16(dst, data []byte) []byte {
	length := []byte{byte(len(data) >> 8), byte(len(data))}
	dst = append(dst, length...)
	return append(dst, data...)
}

type testBitWriter struct {
	data []byte
	bits int
}

func (w *testBitWriter) bit(value uint8) {
	if w.bits%8 == 0 {
		w.data = append(w.data, 0)
	}
	if value != 0 {
		w.data[len(w.data)-1] |= 1 << (7 - w.bits%8)
	}
	w.bits++
}

func (w *testBitWriter) bitsValue(value uint64, count int) {
	for i := count - 1; i >= 0; i-- {
		w.bit(uint8(value >> i & 1))
	}
}

func (w *testBitWriter) ue(value uint64) {
	code := value + 1
	bits := 0
	for n := code; n != 0; n >>= 1 {
		bits++
	}
	for range bits - 1 {
		w.bit(0)
	}
	w.bitsValue(code, bits)
}

func (w *testBitWriter) se(value int64) {
	code := uint64(value * 2)
	if value <= 0 {
		code = uint64(-value * 2)
	} else {
		code = uint64(value*2 - 1)
	}
	w.ue(code)
}

func (w *testBitWriter) rbspStop() {
	w.bit(1)
	for w.bits%8 != 0 {
		w.bit(0)
	}
}

func (w *testBitWriter) bytes() []byte { return append([]byte(nil), w.data...) }
