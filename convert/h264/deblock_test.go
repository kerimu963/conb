package h264

import "testing"

func TestStrongDeblockingFilter(t *testing.T) {
	plane := []uint8{100, 100, 100, 100, 110, 110, 110, 110}
	filterSamples(plane, 3, 4, 1, 51, 0, 0, 4, false)
	want := []uint8{100, 101, 103, 104, 106, 108, 109, 110}
	for i := range plane {
		if plane[i] != want[i] {
			t.Fatalf("filtered[%d] = %d, want %d; plane=%v", i, plane[i], want[i], plane)
		}
	}
}

func TestDeblockingThresholdRejectsLargeEdge(t *testing.T) {
	plane := []uint8{0, 0, 0, 0, 255, 255, 255, 255}
	filterSamples(plane, 3, 4, 1, 51, 0, 0, 4, false)
	for i, value := range plane {
		want := uint8(0)
		if i >= 4 {
			want = 255
		}
		if value != want {
			t.Fatalf("large edge changed: %v", plane)
		}
	}
}

func TestInterBoundaryStrengthDerivation(t *testing.T) {
	info := map[int]interDeblockInfo{0: {}, 1: {}}
	motion := make(map[[2]int]motionInfo)
	for y := range 4 {
		for x := range 8 {
			motion[[2]int{x, y}] = motionInfo{reference: 0}
		}
	}
	_, qBlock := deblockBlockAddress(2, 1, 0)
	entry := info[0]
	entry.lumaNonZero[qBlock] = true
	info[0] = entry
	if got := interLumaStrength(info, motion, 2, 0, 0, 1, 0, false); got != 2 {
		t.Fatalf("coefficient boundary strength = %d, want 2", got)
	}
	entry.lumaNonZero[qBlock] = false
	info[0] = entry
	motion[[2]int{1, 0}] = motionInfo{vector: [2]int{4, 0}, reference: 0}
	if got := interLumaStrength(info, motion, 2, 0, 0, 1, 0, false); got != 1 {
		t.Fatalf("motion boundary strength = %d, want 1", got)
	}
	entry.intra = true
	info[0] = entry
	if got := interLumaStrength(info, motion, 2, 0, 0, 1, 0, false); got != 3 {
		t.Fatalf("internal intra boundary strength = %d, want 3", got)
	}
	if got := interLumaStrength(info, motion, 2, 3, 0, 4, 0, true); got != 4 {
		t.Fatalf("macroblock intra boundary strength = %d, want 4", got)
	}
}

func TestBBoundaryStrengthAcceptsEitherReferencePairing(t *testing.T) {
	info := map[int]interDeblockInfo{0: {}}
	pictureA, pictureB := &Frame420{}, &Frame420{}
	pointP, pointQ := [2]int{0, 0}, [2]int{1, 0}
	motion := [2]map[[2]int]motionInfo{{}, {}}
	motion[0][pointP] = motionInfo{picture: pictureA, vector: [2]int{0, 0}}
	motion[1][pointP] = motionInfo{picture: pictureB, vector: [2]int{8, 0}}
	motion[0][pointQ] = motionInfo{picture: pictureA, vector: [2]int{8, 0}}
	motion[1][pointQ] = motionInfo{picture: pictureB, vector: [2]int{0, 0}}
	if got := interLumaStrengthB(info, motion, 1, 0, 0, 1, 0, false); got != 1 {
		t.Fatalf("different same-order motion strength = %d, want 1", got)
	}

	// When both lists name the same reference picture, the swapped pairing is
	// also valid. Its matching vectors make this a zero-strength boundary.
	motion[1][pointP] = motionInfo{picture: pictureA, vector: [2]int{8, 0}}
	motion[1][pointQ] = motionInfo{picture: pictureA, vector: [2]int{0, 0}}
	if got := interLumaStrengthB(info, motion, 1, 0, 0, 1, 0, false); got != 0 {
		t.Fatalf("matching swapped motion strength = %d, want 0", got)
	}

	motion[0][pointQ] = motionInfo{picture: pictureB, vector: [2]int{0, 0}}
	motion[1][pointQ] = motionInfo{picture: pictureB, vector: [2]int{8, 0}}
	if got := interLumaStrengthB(info, motion, 1, 0, 0, 1, 0, false); got != 1 {
		t.Fatalf("different reference-set strength = %d, want 1", got)
	}
}
