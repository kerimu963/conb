package h264

import "testing"

func TestPredictIntra16x16DC(t *testing.T) {
	neighbours := Intra16Neighbours{TopAvailable: true, LeftAvailable: true}
	for i := range 16 {
		neighbours.Top[i] = 100
		neighbours.Left[i] = 140
	}
	prediction, err := PredictIntra16x16(2, neighbours)
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range prediction {
		if value != 120 {
			t.Errorf("prediction %d = %d, want 120", index, value)
		}
	}
}

func TestReconstructIntra16x16Clips(t *testing.T) {
	var prediction [256]uint8
	for i := range prediction {
		prediction[i] = 250
	}
	var residual [16][16]int64
	for block := range residual {
		for sample := range residual[block] {
			residual[block][sample] = 10
		}
	}
	result := ReconstructIntra16x16(prediction, residual)
	for index, value := range result {
		if value != 255 {
			t.Errorf("reconstructed %d = %d, want 255", index, value)
		}
	}
}

func TestAllIntra4x4ModesPreserveConstantReferences(t *testing.T) {
	neighbours := Intra4Neighbours{
		TopLeft: 100, TopAvailable: true, TopRightAvailable: true,
		LeftAvailable: true, TopLeftAvailable: true,
	}
	for i := range neighbours.Top {
		neighbours.Top[i] = 100
	}
	for i := range neighbours.Left {
		neighbours.Left[i] = 100
	}
	for mode := uint8(0); mode < 9; mode++ {
		prediction, err := PredictIntra4x4(mode, neighbours)
		if err != nil {
			t.Fatalf("mode %d: %v", mode, err)
		}
		for index, value := range prediction {
			if value != 100 {
				t.Errorf("mode %d sample %d = %d, want 100", mode, index, value)
			}
		}
	}
}

func TestAllChromaModesPreserveConstantReferences(t *testing.T) {
	n := ChromaNeighbours420{TopLeft: 90, TopAvailable: true, LeftAvailable: true, TopLeftAvailable: true}
	for i := range 8 {
		n.Top[i], n.Left[i] = 90, 90
	}
	for mode := uint64(0); mode < 4; mode++ {
		prediction, err := PredictChroma420(mode, n)
		if err != nil {
			t.Fatal(err)
		}
		for index, value := range prediction {
			if value != 90 {
				t.Errorf("mode %d sample %d = %d, want 90", mode, index, value)
			}
		}
	}
}
