package h264

import "testing"

func TestCABACResidualContextIntraBoundariesAndPriorBlock(t *testing.T) {
	context, err := NewCABACResidualContext(2)
	if err != nil {
		t.Fatal(err)
	}
	header := MacroblockHeader{Kind: MacroblockIntra4x4, CodedBlockPatternLuma: 15}
	if err = context.BeginMacroblock(0, 0, 0, header, false); err != nil {
		t.Fatal(err)
	}
	models, _ := InitializeEarlyCABACModels(SliceI, 0, 26)
	decoder := &scriptedCABAC{bins: []uint8{0}, models: &models}
	if _, err = context.DecodeBlock(&models, decoder, CABACLuma4x4, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if len(decoder.contexts) != 1 || decoder.contexts[0] != 96 { // 85 + 8 + (1 + 2)
		t.Fatalf("first block context = %v", decoder.contexts)
	}
	increment, err := context.CodedBlockIncrement(CABACLuma4x4, 0, 0, 1, 0)
	if err != nil || increment != 2 { // decoded-zero left, unavailable top for Intra
		t.Fatalf("second block increment = %d, %v", increment, err)
	}
}

func TestCABACResidualContextSliceBoundaryAndPCM(t *testing.T) {
	context, _ := NewCABACResidualContext(2)
	pcm := MacroblockHeader{Kind: MacroblockPCM}
	if err := context.BeginMacroblock(0, 0, 0, pcm, false); err != nil {
		t.Fatal(err)
	}
	inter := MacroblockHeader{Kind: MacroblockInter, CodedBlockPatternLuma: 15}
	if err := context.BeginMacroblock(1, 0, 0, inter, false); err != nil {
		t.Fatal(err)
	}
	increment, err := context.CodedBlockIncrement(CABACLuma4x4, 1, 0, 0, 0)
	if err != nil || increment != 1 { // PCM left, unavailable top for Inter
		t.Fatalf("PCM neighbour increment = %d, %v", increment, err)
	}
}
