package h264

import (
	"fmt"
	"testing"
)

func TestDecodeResidualBlockCABAC(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceI, 0, 26)
	decoder := &scriptedCABAC{
		bins: []uint8{
			1,    // coded_block_flag
			1, 0, // coefficient 0 is significant, not last
			0,    // coefficient 1 is not significant
			1, 1, // coefficient 2 is significant and last
			0,    // reverse coefficient 2 has magnitude 1
			1, 0, // coefficient 0 has magnitude 2
		},
		bypass: []uint8{0, 1},
		models: &models,
	}
	coefficients, err := DecodeResidualBlockCABAC(&models, decoder, CABACLuma4x4, 1)
	if err != nil {
		t.Fatal(err)
	}
	if coefficients[0] != -2 || coefficients[1] != 0 || coefficients[2] != 1 {
		t.Fatalf("coefficients = %v", coefficients)
	}
	wantContexts := "[94 134 195 135 136 197 248 249 252]"
	if got := fmt.Sprint(decoder.contexts); got != wantContexts {
		t.Fatalf("contexts = %s, want %s", got, wantContexts)
	}
}

func TestDecodeResidualBlockCABACNotCoded(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceI, 0, 26)
	decoder := &scriptedCABAC{bins: []uint8{0}, models: &models}
	coefficients, err := DecodeResidualBlockCABAC(&models, decoder, CABACChromaDC, 0)
	if err != nil || len(coefficients) != 4 {
		t.Fatalf("not-coded block = %v, %v", coefficients, err)
	}
	for _, coefficient := range coefficients {
		if coefficient != 0 {
			t.Fatalf("not-coded coefficients = %v", coefficients)
		}
	}
}

func TestDecodeResidualBlockCABACInterIDC0(t *testing.T) {
	models, err := InitializeEarlyCABACModels(SliceP, 0, 31)
	if err != nil {
		t.Fatal(err)
	}
	decoder := &scriptedCABAC{bins: []uint8{0}, models: &models}
	coefficients, err := DecodeResidualBlockCABAC(&models, decoder, CABACLuma4x4, 0)
	if err != nil || len(coefficients) != 16 || len(decoder.contexts) != 1 || decoder.contexts[0] != 93 {
		t.Fatalf("inter residual = %v, contexts %v, error %v", coefficients, decoder.contexts, err)
	}
}

func TestDecodeCABACCoefficientMagnitudeBypassSuffix(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceI, 0, 26)
	decoder := &scriptedCABAC{
		bins:   []uint8{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		bypass: []uint8{1, 0, 0}, // EG0 suffix value 1
		models: &models,
	}
	value, err := decodeCABACCoefficientMagnitude(&models, decoder, 227, 0, 0)
	if err != nil || value != 15 {
		t.Fatalf("coefficient magnitude minus one = %d, %v", value, err)
	}
}

func TestDecodeIntra4x4LumaResidualCABACUncoded(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceI, 0, 26)
	decoder := &scriptedCABAC{models: &models}
	residual, err := DecodeIntra4x4LumaResidualCABAC(&models, decoder, MacroblockHeader{Kind: MacroblockIntra4x4}, [16]int{})
	if err != nil {
		t.Fatal(err)
	}
	for block := range residual.Blocks {
		for _, coefficient := range residual.Blocks[block] {
			if coefficient != 0 {
				t.Fatalf("uncoded residual is non-zero")
			}
		}
	}
}
