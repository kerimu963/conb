package h264

import "testing"

func TestInverseTransform4x4DCOnly(t *testing.T) {
	coefficients := [16]int64{64}
	result, err := InverseTransform4x4(coefficients, 0)
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range result {
		if value != 10 {
			t.Errorf("sample %d = %d, want 10", index, value)
		}
	}
}

func TestDCTransforms(t *testing.T) {
	luma, err := TransformIntra16x16DC([16]int64{64}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range luma {
		if value != 160 {
			t.Errorf("luma DC %d = %d, want 160", index, value)
		}
	}
	chroma, err := TransformChromaDC420([4]int64{1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range chroma {
		if value != 5 {
			t.Errorf("chroma DC %d = %d, want 5", index, value)
		}
	}
	if qp, _ := ChromaQP420(51, 0); qp != 39 {
		t.Fatalf("ChromaQP420(51) = %d, want 39", qp)
	}
}

func TestIntra16x16DCRestoresFlatResidual(t *testing.T) {
	dc, err := TransformIntra16x16DC([16]int64{-795}, 9)
	if err != nil {
		t.Fatal(err)
	}
	block, err := InverseTransform4x4AC([15]int64{}, dc[0], 9)
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range block {
		if value != -87 {
			t.Fatalf("flat residual sample %d = %d, want -87", index, value)
		}
	}
}

func TestInverseTransform4x4RejectsQP(t *testing.T) {
	if _, err := InverseTransform4x4([16]int64{}, -1); err == nil {
		t.Fatal("negative QP was accepted")
	}
}
