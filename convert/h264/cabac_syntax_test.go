package h264

import (
	"fmt"
	"testing"
)

type scriptedCABAC struct {
	bins      []uint8
	terminate []uint8
	bypass    []uint8
	contexts  []int
	models    *CABACModels
}

func (d *scriptedCABAC) DecodeBin(context *CABACContext) (uint8, error) {
	if len(d.bins) == 0 {
		return 0, fmt.Errorf("script exhausted")
	}
	for index := range d.models.Context {
		if context == &d.models.Context[index] {
			d.contexts = append(d.contexts, index)
			break
		}
	}
	result := d.bins[0]
	d.bins = d.bins[1:]
	return result, nil
}

func (d *scriptedCABAC) DecodeTerminate() (uint8, error) {
	if len(d.terminate) == 0 {
		return 0, fmt.Errorf("terminate script exhausted")
	}
	result := d.terminate[0]
	d.terminate = d.terminate[1:]
	return result, nil
}

func (d *scriptedCABAC) DecodeBypass() (uint8, error) {
	if len(d.bypass) == 0 {
		return 0, fmt.Errorf("bypass script exhausted")
	}
	result := d.bypass[0]
	d.bypass = d.bypass[1:]
	return result, nil
}

func TestDecodeCABACMBQPDelta(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceP, 0, 26)
	decoder := &scriptedCABAC{bins: []uint8{1, 1, 1, 0}, models: &models}
	value, err := DecodeCABACMBQPDelta(&models, decoder, true)
	if err != nil || value != 2 {
		t.Fatalf("mb_qp_delta = %d, %v", value, err)
	}
	want := []int{61, 62, 63, 63}
	if fmt.Sprint(decoder.contexts) != fmt.Sprint(want) {
		t.Fatalf("contexts = %v, want %v", decoder.contexts, want)
	}

	models, _ = InitializeEarlyCABACModels(SliceP, 0, 26)
	decoder = &scriptedCABAC{bins: []uint8{1, 1, 0}, models: &models}
	value, err = DecodeCABACMBQPDelta(&models, decoder, false)
	if err != nil || value != -1 {
		t.Fatalf("negative mb_qp_delta = %d, %v", value, err)
	}
}

func TestDecodeCABACPredictionSyntax(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceI, 0, 26)
	decoder := &scriptedCABAC{bins: []uint8{1, 1, 0}, models: &models}
	mode, err := DecodeCABACIntraChromaMode(&models, decoder, true, false)
	if err != nil || mode != 2 || fmt.Sprint(decoder.contexts) != "[65 67 67]" {
		t.Fatalf("chroma mode = %d, contexts %v, error %v", mode, decoder.contexts, err)
	}

	models, _ = InitializeEarlyCABACModels(SliceI, 0, 26)
	decoder = &scriptedCABAC{bins: []uint8{0, 1, 0, 0}, models: &models}
	intra, err := DecodeCABACIntra4x4Mode(&models, decoder)
	if err != nil || intra.Prev || intra.Rem != 1 {
		t.Fatalf("intra mode = %+v, %v", intra, err)
	}
}

func TestDecodeCABACRefIndex(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceP, 0, 26)
	decoder := &scriptedCABAC{bins: []uint8{1, 1, 0}, models: &models}
	index, err := DecodeCABACRefIndex(&models, decoder, 2, 3)
	if err != nil || index != 2 {
		t.Fatalf("ref index = %d, %v", index, err)
	}
	if got := fmt.Sprint(decoder.contexts); got != "[56 58 59]" {
		t.Fatalf("contexts = %s", got)
	}
}

func TestDecodeCABACCodedBlockPattern(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceP, 0, 26)
	decoder := &scriptedCABAC{bins: []uint8{
		1, 0, 1, 0, // luma pattern 0101
		1, 1, // chroma pattern 2
	}, models: &models}
	left := CABACCBPNeighbour{Available: true, Luma: 0b1010, Chroma: 2}
	top := CABACCBPNeighbour{Available: true, Luma: 0b1100, Chroma: 1}
	luma, chroma, err := DecodeCABACCodedBlockPattern(&models, decoder, left, top, true)
	if err != nil || luma != 5 || chroma != 2 {
		t.Fatalf("CBP = (%d,%d), %v", luma, chroma, err)
	}
	// block0: coded left/top => 73; block1: current left coded/top coded;
	// block2: coded external left/current top; block3: current top is not coded.
	// Chroma neighbours yield increments 3 for the first bin and 1 for second.
	want := "[73 73 73 75 80 82]"
	if got := fmt.Sprint(decoder.contexts); got != want {
		t.Fatalf("contexts = %s, want %s", got, want)
	}
}

func TestDecodeCABACIMacroblockTypes(t *testing.T) {
	tests := []struct {
		name      string
		bins      []uint8
		terminate []uint8
		want      uint64
		contexts  string
	}{
		{"I4x4", []uint8{0}, nil, 0, "[5]"},
		{"IPCM", []uint8{1}, []uint8{1}, 25, "[3]"},
		// I_16x16 prediction=3, chroma CBP=2, luma CBP=15 => type 24.
		{"I16x16", []uint8{1, 1, 1, 1, 1, 1}, []uint8{0}, 24, "[4 6 7 8 9 10]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			models, _ := InitializeEarlyCABACModels(SliceI, 0, 26)
			decoder := &scriptedCABAC{bins: append([]uint8(nil), test.bins...), terminate: append([]uint8(nil), test.terminate...), models: &models}
			neighbours := 0
			if test.name == "I4x4" {
				neighbours = 2
			} else if test.name == "I16x16" {
				neighbours = 1
			}
			got, err := DecodeCABACIMacroblockType(&models, decoder, neighbours)
			if err != nil || got != test.want || fmt.Sprint(decoder.contexts) != test.contexts {
				t.Fatalf("type = %d, contexts %v, error %v; want %d, %s", got, decoder.contexts, err, test.want, test.contexts)
			}
		})
	}
}

func TestDecodeCABACPMacroblockTypes(t *testing.T) {
	tests := []struct {
		name      string
		bins      []uint8
		terminate []uint8
		want      uint64
		contexts  string
	}{
		{"P16x16", []uint8{0, 0, 0}, nil, 0, "[14 15 16]"},
		{"P16x8", []uint8{0, 1, 1}, nil, 1, "[14 15 17]"},
		{"P8x16", []uint8{0, 1, 0}, nil, 2, "[14 15 17]"},
		{"P8x8", []uint8{0, 0, 1}, nil, 3, "[14 15 16]"},
		// Prefix 1 followed by I_16x16_0_0_0 (I suffix type 1).
		{"PIntra16", []uint8{1, 1, 0, 0, 0, 0}, []uint8{0}, 6, "[14 17 18 19 21 22]"},
		{"PPCM", []uint8{1, 1}, []uint8{1}, 30, "[14 17]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			models, _ := InitializeEarlyCABACModels(SliceP, 0, 26)
			decoder := &scriptedCABAC{bins: append([]uint8(nil), test.bins...), terminate: append([]uint8(nil), test.terminate...), models: &models}
			got, err := DecodeCABACPMacroblockType(&models, decoder)
			if err != nil || got != test.want || fmt.Sprint(decoder.contexts) != test.contexts {
				t.Fatalf("type = %d, contexts %v, error %v; want %d, %s", got, decoder.contexts, err, test.want, test.contexts)
			}
		})
	}
}

func TestDecodeCABACBAndSubMacroblockTypes(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceB, 0, 26)
	decoder := &scriptedCABAC{bins: []uint8{1, 1, 1, 0, 1, 1, 0}, models: &models}
	value, err := DecodeCABACBMacroblockType(&models, decoder, 2)
	if err != nil || value != 18 || fmt.Sprint(decoder.contexts) != "[29 30 31 32 32 32 32]" {
		t.Fatalf("B mb type = %d, contexts %v, error %v", value, decoder.contexts, err)
	}
	models, _ = InitializeEarlyCABACModels(SliceB, 0, 26)
	decoder = &scriptedCABAC{bins: []uint8{1, 0, 0}, models: &models}
	value, err = DecodeCABACBMacroblockType(&models, decoder, 0)
	if err != nil || value != 1 || fmt.Sprint(decoder.contexts) != "[27 30 32]" {
		t.Fatalf("B L0 mb type = %d, contexts %v, error %v", value, decoder.contexts, err)
	}
	models, _ = InitializeEarlyCABACModels(SliceB, 0, 26)
	decoder = &scriptedCABAC{bins: []uint8{
		1, 1, 1, 1, 0, 1, // B intra prefix
		1, 0, 0, 0, 0, // I_16x16_0_0_0 suffix
	}, terminate: []uint8{0}, models: &models}
	value, err = DecodeCABACBMacroblockType(&models, decoder, 0)
	if err != nil || value != 24 {
		t.Fatalf("B intra type = %d, contexts %v, error %v", value, decoder.contexts, err)
	}

	models, _ = InitializeEarlyCABACModels(SliceP, 0, 26)
	decoder = &scriptedCABAC{bins: []uint8{0, 1, 1}, models: &models}
	value, err = DecodeCABACSubMacroblockType(&models, decoder, SliceP)
	if err != nil || value != 2 || fmt.Sprint(decoder.contexts) != "[21 22 23]" {
		t.Fatalf("P sub type = %d, contexts %v, error %v", value, decoder.contexts, err)
	}

	models, _ = InitializeEarlyCABACModels(SliceB, 0, 26)
	decoder = &scriptedCABAC{bins: []uint8{1, 1, 1, 1, 0}, models: &models}
	value, err = DecodeCABACSubMacroblockType(&models, decoder, SliceB)
	if err != nil || value != 11 || fmt.Sprint(decoder.contexts) != "[36 37 38 39 39]" {
		t.Fatalf("B sub type = %d, contexts %v, error %v", value, decoder.contexts, err)
	}
	models, _ = InitializeEarlyCABACModels(SliceB, 0, 26)
	decoder = &scriptedCABAC{bins: []uint8{1, 0, 0}, models: &models}
	value, err = DecodeCABACSubMacroblockType(&models, decoder, SliceB)
	if err != nil || value != 1 || fmt.Sprint(decoder.contexts) != "[36 37 39]" {
		t.Fatalf("B L0 sub type = %d, contexts %v, error %v", value, decoder.contexts, err)
	}
}

func TestDecodeCABACMVD(t *testing.T) {
	models, _ := InitializeEarlyCABACModels(SliceP, 0, 26)
	decoder := &scriptedCABAC{bins: []uint8{1, 1, 0}, bypass: []uint8{0}, models: &models}
	value, err := DecodeCABACMVD(&models, decoder, false, 2, 3)
	if err != nil || value != 2 || fmt.Sprint(decoder.contexts) != "[41 43 44]" {
		t.Fatalf("MVD = %d, contexts %v, error %v", value, decoder.contexts, err)
	}

	models, _ = InitializeEarlyCABACModels(SliceP, 0, 26)
	decoder = &scriptedCABAC{
		bins:   []uint8{1, 1, 1, 1, 1, 1, 1, 1, 1},
		bypass: []uint8{0, 0, 0, 1, 1}, // EG3 suffix 1, then negative sign
		models: &models,
	}
	value, err = DecodeCABACMVD(&models, decoder, false, 0, 0)
	if err != nil || value != -10 {
		t.Fatalf("large MVD = %d, contexts %v, error %v", value, decoder.contexts, err)
	}
}

func TestDecodeCABACUEGSuffixAddsPrefixBaseAndInfo(t *testing.T) {
	// UEG0: prefix 1, terminator 0, info 1 represents 2.  Using bitwise OR
	// for the prefix base and info bit incorrectly collapsed this to 1.
	decoder := &scriptedCABAC{bypass: []uint8{1, 0, 1}}
	value, err := decodeCABACUEGSuffix(decoder, 0)
	if err != nil || value != 2 {
		t.Fatalf("UEG0 suffix = %d, want 2 (error %v)", value, err)
	}
}
