package h264

import "testing"

func cabacReader(t *testing.T, offset uint64, trailingBits ...uint8) *BitReader {
	t.Helper()
	w := &testBitWriter{}
	w.bitsValue(offset, 9)
	for _, bit := range trailingBits {
		w.bit(bit)
	}
	for w.bits%8 != 0 {
		w.bit(0)
	}
	r, err := NewBitReader(w.bytes())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCABACContextInitialization(t *testing.T) {
	if got := NewCABACContext(20, -15, 26); got != (CABACContext{State: 46, MPS: 0}) {
		t.Fatalf("context 1 = %+v", got)
	}
	if got := NewCABACContext(-3, 74, 26); got != (CABACContext{State: 5, MPS: 1}) {
		t.Fatalf("context 2 = %+v", got)
	}
}

func TestCABACMPSAndLPSStateTransitions(t *testing.T) {
	decoder, err := NewCABACDecoder(cabacReader(t, 0, 0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	context := CABACContext{}
	bin, err := decoder.DecodeBin(&context)
	if err != nil || bin != 0 || context.State != 1 || context.MPS != 0 || decoder.rangeV != 270 {
		t.Fatalf("MPS decode = (bin %d context %+v range %d err %v)", bin, context, decoder.rangeV, err)
	}

	decoder, err = NewCABACDecoder(cabacReader(t, 400, 0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	context = CABACContext{}
	bin, err = decoder.DecodeBin(&context)
	if err != nil || bin != 1 || context.State != 0 || context.MPS != 1 || decoder.rangeV != 480 || decoder.offset != 260 {
		t.Fatalf("LPS decode = (bin %d context %+v range %d offset %d err %v)", bin, context, decoder.rangeV, decoder.offset, err)
	}
}

func TestCABACAlignmentAndTerminate(t *testing.T) {
	w := &testBitWriter{}
	w.bitsValue(0, 3)
	for range 5 {
		w.bit(1)
	}
	w.bitsValue(509, 9)
	for w.bits%8 != 0 {
		w.bit(0)
	}
	r, _ := NewBitReader(w.bytes())
	r.SkipBits(3)
	decoder, err := NewCABACDecoder(r)
	if err != nil {
		t.Fatal(err)
	}
	bin, err := decoder.DecodeTerminate()
	if err != nil || bin != 1 {
		t.Fatalf("terminate = (%d,%v)", bin, err)
	}
}

func TestCABACEarlyContextTables(t *testing.T) {
	models, err := InitializeEarlyCABACModels(SliceP, 0, 26)
	if err != nil {
		t.Fatal(err)
	}
	if models.Context[0] != (CABACContext{State: 46}) ||
		models.Context[11] != (CABACContext{State: 6, MPS: 1}) ||
		models.Context[60] != (CABACContext{State: 22}) ||
		models.Context[276] != (CABACContext{State: 63}) {
		t.Fatalf("selected early contexts = (%+v %+v %+v %+v)", models.Context[0], models.Context[11], models.Context[60], models.Context[276])
	}
	if !models.Ready[0] || !models.Ready[69] || !models.Ready[70] || !models.Ready[104] || !models.Ready[276] {
		t.Fatalf("early context readiness is incorrect")
	}
	if models.Context[70] != (CABACContext{State: 18}) || models.Context[104] != (CABACContext{State: 11, MPS: 1}) {
		t.Fatalf("Table 9-18 contexts = (%+v,%+v)", models.Context[70], models.Context[104])
	}
	if !models.Ready[105] || !models.Ready[275] || models.Context[105] != NewCABACContext(-2, 85, 26) ||
		models.Context[275] != NewCABACContext(-8, 85, 26) {
		t.Fatalf("inter IDC 0 residual contexts are incorrect")
	}
	inter1, err := InitializeEarlyCABACModels(SliceP, 1, 26)
	if err != nil || !inter1.Ready[105] || !inter1.Ready[275] ||
		inter1.Context[105] != NewCABACContext(-13, 103, 26) || inter1.Context[275] != NewCABACContext(-4, 78, 26) {
		t.Fatalf("inter IDC 1 residual contexts are incorrect")
	}
	inter2, err := InitializeEarlyCABACModels(SliceB, 2, 26)
	if err != nil || !inter2.Ready[105] || !inter2.Ready[275] ||
		inter2.Context[105] != NewCABACContext(-4, 86, 26) || inter2.Context[275] != NewCABACContext(-10, 87, 26) {
		t.Fatalf("inter IDC 2 residual contexts are incorrect")
	}
	intra, err := InitializeEarlyCABACModels(SliceI, 2, 26)
	if err != nil || intra.Ready[11] || !intra.Ready[60] {
		t.Fatalf("I-slice context selection = (ready11 %v ready60 %v err %v)", intra.Ready[11], intra.Ready[60], err)
	}
	if !intra.Ready[105] || !intra.Ready[165] || !intra.Ready[166] || !intra.Ready[226] ||
		!intra.Ready[227] || !intra.Ready[275] || intra.Ready[277] {
		t.Fatalf("I residual context readiness is incorrect")
	}
	if intra.Context[105] != NewCABACContext(-7, 93, 26) || intra.Context[165] != NewCABACContext(12, 72, 26) {
		t.Fatalf("I significant-coefficient contexts are incorrect")
	}
	if intra.Context[166] != NewCABACContext(24, 0, 26) || intra.Context[226] != NewCABACContext(2, 97, 26) ||
		intra.Context[227] != NewCABACContext(-3, 71, 26) || intra.Context[275] != NewCABACContext(-14, 97, 26) {
		t.Fatalf("I last/level coefficient contexts are incorrect")
	}
}

func TestDecodeCABACSinglePSkipPicture(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	pps, _ := store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	pps.EntropyCodingCABAC = true
	store.pictures[pps.ID] = pps

	w := &testBitWriter{}
	w.ue(0)
	w.ue(0) // P slice
	w.ue(0)
	w.bitsValue(1, 4)
	w.bitsValue(2, 4)
	w.bit(0) // reference count override
	w.bit(0) // list modification
	w.bit(0) // adaptive reference marking
	w.ue(0)  // cabac_init_idc
	w.se(0)
	w.ue(1) // disable deblocking
	for w.bits%8 != 0 {
		w.bit(1) // cabac_alignment_one_bit
	}
	// At QP 26, ctxIdx 11 decodes P_Skip as MPS for offset 333;
	// the remaining range then makes terminate decode to one.
	w.bitsValue(333, 9)
	for w.bits%8 != 0 {
		w.bit(0)
	}
	unit, err := ParseNALUnit(append([]byte{0x41}, w.bytes()...))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodeCABACPSkipFrame([]NALUnit{unit}, store, []*Frame420{solidReference(90)})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Y[0] != 90 || frame.Y[len(frame.Y)-1] != 90 {
		t.Fatalf("CABAC P_Skip endpoints = (%d,%d)", frame.Y[0], frame.Y[len(frame.Y)-1])
	}
}

func TestDecodeCABACIThenPCMRestart(t *testing.T) {
	store := NewParameterSetStore()
	spsUnit, _ := ParseNALUnit(baselineSPS())
	ppsUnit, _ := ParseNALUnit(baselinePPS())
	sps, _ := store.AddSPS(spsUnit)
	pps, _ := store.AddPPS(ppsUnit)
	sps.CodedWidth, sps.CodedHeight, sps.Width, sps.Height = 16, 16, 16, 16
	store.sequences[sps.ID] = sps
	pps.EntropyCodingCABAC = true
	store.pictures[pps.ID] = pps
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
	w.ue(1)
	for w.bits%8 != 0 {
		w.bit(1)
	}
	// ctxIdx 3 LPS selects the I branch; 1110 renormalization bits
	// leave offset equal to range-2, selecting the PCM terminate bin.
	w.bitsValue(509, 9)
	w.bitsValue(0b1110, 4)
	for w.bits%8 != 0 {
		w.bit(0) // pcm_alignment_zero_bit
	}
	for range 384 {
		w.bitsValue(128, 8)
	}
	// Arithmetic engine restart followed immediately by end_of_slice_flag.
	w.bitsValue(508, 9)
	for w.bits%8 != 0 {
		w.bit(0)
	}
	unit, err := ParseNALUnit(append([]byte{0x65}, w.bytes()...))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodeCABACIPCMFrame([]NALUnit{unit}, store)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Y[0] != 128 || frame.Cb[0] != 128 || frame.Cr[0] != 128 {
		t.Fatalf("CABAC I_PCM samples = (%d,%d,%d)", frame.Y[0], frame.Cb[0], frame.Cr[0])
	}
}
