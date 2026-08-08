package h264

import "testing"

func TestDecodeCABACI16MacroblockHeader(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceI, 0, 26)
	// I_16x16 type 1, chroma mode 0, mb_qp_delta 0.
	decoder := &scriptedCABAC{
		bins:      []uint8{1, 0, 0, 0, 0, 0, 0, 0},
		terminate: []uint8{0},
		models:    &models,
	}
	slice := SliceHeader{SPS: SPS{ChromaFormat: 1, BitDepthLuma: 8}}
	header, err := DecodeCABACIMacroblockHeader(&models, decoder, slice, CABACMacroblockNeighbour{}, CABACMacroblockNeighbour{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if header.RawType != 1 || header.Kind != MacroblockIntra16x16 || header.Intra16x16Prediction != 0 ||
		header.CodedBlockPatternLuma != 0 || header.CodedBlockPatternChroma != 0 || header.QPDelta != 0 {
		t.Fatalf("header = %+v", header)
	}
}

func TestDecodeCABACP16x16MacroblockHeader(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceP, 0, 26)
	decoder := &scriptedCABAC{
		bins: []uint8{
			0, 0, 0, // P_L0_16x16
			0, 0, // zero horizontal/vertical MVD
			0, 0, 0, 0, 0, // zero luma/chroma CBP
		},
		models: &models,
	}
	slice := SliceHeader{ReferenceCount: [2]uint64{1, 0}, SPS: SPS{ChromaFormat: 1}}
	header, err := DecodeCABACP16x16MacroblockHeader(&models, decoder, slice, CABACMacroblockNeighbour{}, CABACMacroblockNeighbour{}, 0, [2][2]int{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if header.Kind != MacroblockInter || len(header.InterPartitions) != 1 || header.CodedBlockPatternLuma != 0 ||
		header.CodedBlockPatternChroma != 0 || header.MotionVectorDifference != [2]int64{} {
		t.Fatalf("header = %+v", header)
	}
}

func TestDecodeCABACP16x8MacroblockHeader(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceP, 0, 26)
	decoder := &scriptedCABAC{
		bins: []uint8{
			0, 1, 1, // P_16x8
			0, 0, 0, 0, // two zero MVD pairs
			0, 0, 0, 0, 0, // zero CBP
		},
		models: &models,
	}
	slice := SliceHeader{ReferenceCount: [2]uint64{1}, SPS: SPS{ChromaFormat: 1}}
	header, err := DecodeCABACPMacroblockHeader(&models, decoder, slice, CABACMacroblockNeighbour{}, CABACMacroblockNeighbour{}, [16]int{}, [16][2][2]int{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if header.RawType != 1 || len(header.InterPartitions) != 2 || header.InterPartitions[0].Height != 8 || header.InterPartitions[1].Y != 8 {
		t.Fatalf("header = %+v", header)
	}
}

func TestDecodeCABACP8x8MacroblockHeader(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceP, 0, 26)
	decoder := &scriptedCABAC{
		bins: []uint8{
			0, 0, 1, // P_8x8
			1, 1, 1, 1, // four P_L0_8x8 sub types
			0, 0, 0, 0, 0, 0, 0, 0, // four zero MVD pairs
			0, 0, 0, 0, 0, // zero CBP
		},
		models: &models,
	}
	slice := SliceHeader{ReferenceCount: [2]uint64{1}, SPS: SPS{ChromaFormat: 1}}
	header, err := DecodeCABACPMacroblockHeader(&models, decoder, slice, CABACMacroblockNeighbour{}, CABACMacroblockNeighbour{}, [16]int{}, [16][2][2]int{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if header.RawType != 3 || len(header.InterPartitions) != 4 {
		t.Fatalf("header = %+v", header)
	}
	for _, partition := range header.InterPartitions {
		if partition.Width != 8 || partition.Height != 8 {
			t.Fatalf("partition = %+v", partition)
		}
	}
}

func TestDecodeCABACB16x16MacroblockHeader(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceB, 0, 26)
	decoder := &scriptedCABAC{
		bins: []uint8{
			1, 0, 0, // B_L0_16x16
			0, 0, // zero list-0 MVD
			0, 0, 0, 0, 0, // zero luma/chroma CBP
		},
		models: &models,
	}
	slice := SliceHeader{ReferenceCount: [2]uint64{1, 1}, SPS: SPS{ChromaFormat: 1}}
	header, err := decodeCABACBMacroblockHeaderWithContexts(&models, decoder, slice,
		CABACMacroblockNeighbour{}, CABACMacroblockNeighbour{}, 0, false,
		func(int, int, InterPartition) (int, [2][2]int) { return 0, [2][2]int{} })
	if err != nil {
		t.Fatal(err)
	}
	if header.RawType != 1 || len(header.InterPartitions) != 1 || !header.InterPartitions[0].UseList0 ||
		header.InterPartitions[0].UseList1 || header.InterPartitions[0].MotionDifference != [2]int64{} {
		t.Fatalf("header = %+v", header)
	}
}

func TestDecodeCABACB8x8MacroblockHeader(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceB, 0, 26)
	bins := []uint8{1, 1, 1, 1, 1, 1} // B_8x8
	for range 4 {
		bins = append(bins, 1, 0, 0) // B_L0_8x8
	}
	for range 4 {
		bins = append(bins, 0, 0) // zero list-0 MVD
	}
	bins = append(bins, 0, 0, 0, 0, 0) // zero CBP
	decoder := &scriptedCABAC{bins: bins, models: &models}
	slice := SliceHeader{ReferenceCount: [2]uint64{1, 1}, SPS: SPS{ChromaFormat: 1}}
	header, err := decodeCABACBMacroblockHeaderWithContexts(&models, decoder, slice,
		CABACMacroblockNeighbour{}, CABACMacroblockNeighbour{}, 0, false,
		func(int, int, InterPartition) (int, [2][2]int) { return 0, [2][2]int{} })
	if err != nil {
		t.Fatal(err)
	}
	if header.RawType != 22 || len(header.InterPartitions) != 4 {
		t.Fatalf("header = %+v", header)
	}
	for _, partition := range header.InterPartitions {
		if partition.Width != 8 || partition.Height != 8 || !partition.UseList0 || partition.UseList1 {
			t.Fatalf("partition = %+v", partition)
		}
	}
}

func TestDecodeCABACBIntra4x4MacroblockHeader(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceB, 0, 26)
	bins := []uint8{1, 1, 1, 1, 0, 1, 0} // B I-prefix, then I_NxN suffix
	for range 16 {
		bins = append(bins, 1) // prev_intra4x4_pred_mode_flag
	}
	bins = append(bins, 0, 0, 0, 0, 0, 0) // chroma mode and zero CBP
	decoder := &scriptedCABAC{bins: bins, models: &models}
	slice := SliceHeader{ReferenceCount: [2]uint64{1, 1}, SPS: SPS{ChromaFormat: 1}}
	header, err := decodeCABACBMacroblockHeaderWithContexts(&models, decoder, slice,
		CABACMacroblockNeighbour{}, CABACMacroblockNeighbour{}, 0, false,
		func(int, int, InterPartition) (int, [2][2]int) { return 0, [2][2]int{} })
	if err != nil {
		t.Fatal(err)
	}
	if header.RawType != 23 || header.Kind != MacroblockIntra4x4 || header.CodedBlockPatternLuma != 0 || header.CodedBlockPatternChroma != 0 {
		t.Fatalf("header = %+v", header)
	}
}
