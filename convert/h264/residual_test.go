package h264

import "testing"

func TestCAVLCBlockContextNC(t *testing.T) {
	context, _ := NewCAVLCBlockContext(2)
	context.setTotalCoeff(0, 0, 2, 3) // left of block 3
	context.setTotalCoeff(0, 0, 1, 6) // top of block 3
	nC, err := context.NC(0, 0, 3)
	if err != nil || nC != 5 {
		t.Fatalf("NC = (%d, %v), want 5", nC, err)
	}
}

func TestDecodeIntra4x4LumaResidual(t *testing.T) {
	context, _ := NewCAVLCBlockContext(1)
	header := MacroblockHeader{Kind: MacroblockIntra4x4, CodedBlockPatternLuma: 1}
	// The first 8x8 group contains four coded blocks. coeff_token "1" means
	// TotalCoeff=0 for their initial nC=0 contexts.
	residual, err := DecodeIntra4x4LumaResidual(readerFromBits("1111"), header, context, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if residual.Blocks[0] != ([16]int64{}) || residual.Blocks[15] != ([16]int64{}) {
		t.Fatalf("zero residual was not preserved: %+v", residual)
	}
	for block := 0; block < 16; block++ {
		nC, err := context.NC(0, 0, block)
		if err != nil || nC != 0 {
			t.Fatalf("block %d NC = (%d, %v), want 0", block, nC, err)
		}
	}
}

func TestDecodeIntra16x16AndChromaResidual(t *testing.T) {
	context, _ := NewCAVLCBlockContext(1)
	header := MacroblockHeader{
		Kind:                    MacroblockIntra16x16,
		CodedBlockPatternLuma:   15,
		CodedBlockPatternChroma: 2,
	}
	// One zero luma DC token and sixteen zero AC tokens.
	luma, err := DecodeIntra16x16LumaResidual(readerFromBits("11111111111111111"), header, context, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if luma.DC != ([16]int64{}) || luma.AC[15] != ([15]int64{}) {
		t.Fatalf("unexpected luma residual: %+v", luma)
	}
	// Cb/Cr DC zero tokens are "01"; their eight AC blocks use nC=0 "1".
	chroma, err := DecodeChromaResidual420(readerFromBits("0101"+"11111111"), header, context, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if chroma.DC[0] != ([4]int64{}) || chroma.AC[1][3] != ([15]int64{}) {
		t.Fatalf("unexpected chroma residual: %+v", chroma)
	}
}
