package h264

import "testing"

func solidReference(value uint8) *Frame420 {
	frame, _ := NewFrame420(16, 16)
	for i := range frame.Y {
		frame.Y[i] = value
	}
	for i := range frame.Cb {
		frame.Cb[i], frame.Cr[i] = 128, 128
	}
	return frame
}

func TestDefaultAndModifiedPReferenceList(t *testing.T) {
	first, second, long := solidReference(10), solidReference(20), solidReference(30)
	decoder := &Decoder{dpb: []referencePicture{
		{frame: first, frameNumber: 1}, {frame: long, longTerm: true, longTermIndex: 0}, {frame: second, frameNumber: 2},
	}}
	header := SliceHeader{FrameNumber: 3, ReferenceCount: [2]uint64{3}, SPS: SPS{Log2MaxFrameNum: 4}}
	list, err := decoder.buildPReferenceList(header)
	if err != nil {
		t.Fatal(err)
	}
	if list[0] != second || list[1] != first || list[2] != long {
		t.Fatalf("default reference order is incorrect")
	}
	header.ReferenceModifications = []ReferenceModification{{List: 0, IDC: 0, Value: 1}}
	list, err = decoder.buildPReferenceList(header)
	if err != nil {
		t.Fatal(err)
	}
	if list[0] != first || list[1] != second || list[2] != long {
		t.Fatalf("modified reference order is incorrect")
	}
}

func TestActiveReferenceCountMayExceedCurrentDPB(t *testing.T) {
	reference := solidReference(10)
	decoder := &Decoder{dpb: []referencePicture{{frame: reference, frameNumber: 0, poc: 0}}}
	header := SliceHeader{
		FrameNumber: 1, PictureOrderCountLSB: 2, ReferenceCount: [2]uint64{4, 4},
		SPS: SPS{Log2MaxFrameNum: 4},
	}
	list, err := decoder.buildPReferenceList(header)
	if err != nil || len(list) != 1 || list[0] != reference {
		t.Fatalf("P reference list = (%v, %v)", list, err)
	}
	lists, err := decoder.buildBReferenceLists(header)
	if err != nil || len(lists[0]) != 1 || len(lists[1]) != 1 || lists[0][0] != reference || lists[1][0] != reference {
		t.Fatalf("B reference lists = (%v, %v)", lists, err)
	}
}

func TestRepeatedReferenceModificationCreatesDuplicatePrefix(t *testing.T) {
	three, five, six := solidReference(30), solidReference(50), solidReference(60)
	decoder := &Decoder{dpb: []referencePicture{
		{frame: three, frameNumber: 3}, {frame: five, frameNumber: 5}, {frame: six, frameNumber: 6},
	}}
	header := SliceHeader{
		FrameNumber: 7, ReferenceCount: [2]uint64{4}, SPS: SPS{Log2MaxFrameNum: 4},
		ReferenceModifications: []ReferenceModification{
			{List: 0, IDC: 0, Value: 1},  // 7 - 2 = frame 5
			{List: 0, IDC: 0, Value: 15}, // 5 - 16 = frame 5 again
			{List: 0, IDC: 1, Value: 0},  // frame 6
			{List: 0, IDC: 0, Value: 2},  // frame 3
		},
	}
	list, err := decoder.buildPReferenceList(header)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 4 || list[0] != five || list[1] != five || list[2] != six || list[3] != three {
		t.Fatalf("modified duplicate reference list = %v", list)
	}
}

func TestDPBSlidingWindowAndMMCO(t *testing.T) {
	decoder := &Decoder{dpb: []referencePicture{
		{frame: solidReference(1), frameNumber: 0}, {frame: solidReference(2), frameNumber: 1},
	}}
	header := SliceHeader{FrameNumber: 2, SPS: SPS{Log2MaxFrameNum: 4, MaxReferenceFrames: 2}}
	if err := decoder.markReference(solidReference(3), header, false); err != nil {
		t.Fatal(err)
	}
	if len(decoder.dpb) != 2 || decoder.dpb[0].frameNumber != 1 || decoder.dpb[1].frameNumber != 2 {
		t.Fatalf("sliding DPB = %+v", decoder.dpb)
	}
	header.FrameNumber = 3
	header.AdaptiveReferenceMarking = true
	header.MemoryManagement = []MemoryManagementControl{{Operation: 1, Argument1: 1}, {Operation: 6, Argument1: 0}}
	if err := decoder.markReference(solidReference(4), header, false); err != nil {
		t.Fatal(err)
	}
	if len(decoder.dpb) != 2 || decoder.dpb[0].frameNumber != 2 || !decoder.dpb[1].longTerm || decoder.dpb[1].longTermIndex != 0 {
		t.Fatalf("MMCO DPB = %+v", decoder.dpb)
	}
}

func TestBReferenceListsUsePictureOrder(t *testing.T) {
	past, future := solidReference(20), solidReference(220)
	decoder := &Decoder{dpb: []referencePicture{
		{frame: future, frameNumber: 2, poc: 4}, {frame: past, frameNumber: 0, poc: 0},
	}}
	header := SliceHeader{
		FrameNumber: 1, PictureOrderCountLSB: 2, ReferenceCount: [2]uint64{2, 2},
		SPS: SPS{Log2MaxFrameNum: 4},
	}
	lists, err := decoder.buildBReferenceLists(header)
	if err != nil {
		t.Fatal(err)
	}
	if lists[0][0] != past || lists[0][1] != future || lists[1][0] != future || lists[1][1] != past {
		t.Fatalf("B reference list ordering is incorrect")
	}
}
