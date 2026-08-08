package h264

import (
	"fmt"
	"sort"
)

type referencePicture struct {
	frame         *Frame420
	frameNumber   uint64
	longTerm      bool
	longTermIndex uint64
	poc           int64
}

func (d *Decoder) buildBReferenceLists(header SliceHeader) ([2][]*Frame420, error) {
	var result [2][]*Frame420
	if len(d.dpb) == 0 {
		return result, fmt.Errorf("B picture has an empty decoded picture buffer")
	}
	currentPOC := int64(header.PictureOrderCountLSB)
	var before, after, long []referencePicture
	for _, picture := range d.dpb {
		if picture.longTerm {
			long = append(long, picture)
		} else if picture.poc < currentPOC {
			before = append(before, picture)
		} else {
			after = append(after, picture)
		}
	}
	sort.Slice(before, func(i, j int) bool { return before[i].poc > before[j].poc })
	sort.Slice(after, func(i, j int) bool { return after[i].poc < after[j].poc })
	sort.Slice(long, func(i, j int) bool { return long[i].longTermIndex < long[j].longTermIndex })
	list0 := append(append(append([]referencePicture(nil), before...), after...), long...)
	list1 := append(append(append([]referencePicture(nil), after...), before...), long...)
	if len(list1) > 1 && sameReferenceOrder(list0, list1) {
		list1[0], list1[1] = list1[1], list1[0]
	}
	var err error
	if list0, err = modifyReferenceList(list0, header, 0); err != nil {
		return result, err
	}
	if list1, err = modifyReferenceList(list1, header, 1); err != nil {
		return result, err
	}
	for listIndex, list := range [][]referencePicture{list0, list1} {
		wanted := int(header.ReferenceCount[listIndex])
		if wanted <= 0 {
			wanted = 1
		}
		// num_ref_idx_active may exceed the pictures currently present in the
		// DPB. Entries beyond the constructed list are unavailable; a conforming
		// macroblock must not select them.
		if wanted > len(list) {
			wanted = len(list)
		}
		result[listIndex] = make([]*Frame420, wanted)
		for i := range result[listIndex] {
			result[listIndex][i] = list[i].frame
		}
	}
	return result, nil
}

func sameReferenceOrder(a, b []referencePicture) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].frame != b[i].frame {
			return false
		}
	}
	return true
}

func modifyReferenceList(list []referencePicture, header SliceHeader, listIndex uint8) ([]referencePicture, error) {
	maximum := uint64(1) << header.SPS.Log2MaxFrameNum
	predictor, insertion := header.FrameNumber, 0
	for _, operation := range header.ReferenceModifications {
		if operation.List != listIndex {
			continue
		}
		target := -1
		if operation.IDC == 0 {
			predictor = (predictor + maximum - (operation.Value+1)%maximum) % maximum
		} else if operation.IDC == 1 {
			predictor = (predictor + operation.Value + 1) % maximum
		}
		for i, picture := range list {
			if operation.IDC == 2 && picture.longTerm && picture.longTermIndex == operation.Value ||
				operation.IDC < 2 && !picture.longTerm && picture.frameNumber == predictor {
				target = i
				break
			}
		}
		if target < 0 {
			return nil, fmt.Errorf("B reference-list modification cannot resolve list %d IDC %d value %d", listIndex, operation.IDC, operation.Value)
		}
		selected := list[target]
		list = insertModifiedReference(list, selected, insertion)
		insertion++
	}
	return list, nil
}

func (d *Decoder) buildPReferenceList(header SliceHeader) ([]*Frame420, error) {
	if len(d.dpb) == 0 {
		return nil, fmt.Errorf("P picture has an empty decoded picture buffer")
	}
	maxFrameNumber := uint64(1) << header.SPS.Log2MaxFrameNum
	list := append([]referencePicture(nil), d.dpb...)
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].longTerm != list[j].longTerm {
			return !list[i].longTerm
		}
		if list[i].longTerm {
			return list[i].longTermIndex < list[j].longTermIndex
		}
		return wrappedPictureNumber(list[i].frameNumber, header.FrameNumber, maxFrameNumber) >
			wrappedPictureNumber(list[j].frameNumber, header.FrameNumber, maxFrameNumber)
	})
	pictureNumberPredictor := header.FrameNumber
	insertion := 0
	for _, operation := range header.ReferenceModifications {
		if operation.List != 0 {
			continue
		}
		var target int = -1
		switch operation.IDC {
		case 0:
			pictureNumberPredictor = (pictureNumberPredictor + maxFrameNumber - (operation.Value+1)%maxFrameNumber) % maxFrameNumber
			for i := range list {
				if !list[i].longTerm && list[i].frameNumber == pictureNumberPredictor {
					target = i
					break
				}
			}
		case 1:
			pictureNumberPredictor = (pictureNumberPredictor + operation.Value + 1) % maxFrameNumber
			for i := range list {
				if !list[i].longTerm && list[i].frameNumber == pictureNumberPredictor {
					target = i
					break
				}
			}
		case 2:
			for i := range list {
				if list[i].longTerm && list[i].longTermIndex == operation.Value {
					target = i
					break
				}
			}
		}
		if target < 0 {
			return nil, fmt.Errorf("reference-list modification cannot resolve IDC %d value %d", operation.IDC, operation.Value)
		}
		selected := list[target]
		list = insertModifiedReference(list, selected, insertion)
		insertion++
	}
	wanted := int(header.ReferenceCount[0])
	if wanted <= 0 {
		wanted = 1
	}
	if wanted > len(list) {
		wanted = len(list)
	}
	result := make([]*Frame420, wanted)
	for i := range result {
		result[i] = list[i].frame
	}
	return result, nil
}

// insertModifiedReference implements the insertion/removal step from
// 8.2.4.3.  Only a duplicate after the insertion point is removed. Repeating
// a modification for a picture already in the constructed prefix therefore
// intentionally creates another active reference-list entry.
func insertModifiedReference(list []referencePicture, selected referencePicture, insertion int) []referencePicture {
	if insertion > len(list) {
		insertion = len(list)
	}
	list = append(list, referencePicture{})
	copy(list[insertion+1:], list[insertion:])
	list[insertion] = selected
	for index := insertion + 1; index < len(list); index++ {
		if list[index].frame == selected.frame {
			copy(list[index:], list[index+1:])
			list = list[:len(list)-1]
			break
		}
	}
	return list
}

func wrappedPictureNumber(frameNumber, current, maximum uint64) int64 {
	if frameNumber > current {
		return int64(frameNumber) - int64(maximum)
	}
	return int64(frameNumber)
}

func (d *Decoder) markReference(frame *Frame420, header SliceHeader, idr bool) error {
	if idr {
		d.dpb = nil
		picture := referencePicture{frame: frame, frameNumber: header.FrameNumber, poc: int64(header.PictureOrderCountLSB)}
		if header.LongTermReference {
			picture.longTerm = true
			frame.longTerm = true
		}
		d.dpb = append(d.dpb, picture)
		return nil
	}
	currentLongTerm := false
	currentLongTermIndex := uint64(0)
	if header.AdaptiveReferenceMarking {
		for _, operation := range header.MemoryManagement {
			switch operation.Operation {
			case 1:
				difference := operation.Argument1 + 1
				maxFrameNumber := uint64(1) << header.SPS.Log2MaxFrameNum
				target := (header.FrameNumber + maxFrameNumber - difference%maxFrameNumber) % maxFrameNumber
				d.removeShortTerm(target)
			case 2:
				d.removeLongTerm(operation.Argument1)
			case 3:
				difference := operation.Argument1 + 1
				maxFrameNumber := uint64(1) << header.SPS.Log2MaxFrameNum
				target := (header.FrameNumber + maxFrameNumber - difference%maxFrameNumber) % maxFrameNumber
				d.removeLongTerm(operation.Argument2)
				found := false
				for i := range d.dpb {
					if !d.dpb[i].longTerm && d.dpb[i].frameNumber == target {
						d.dpb[i].longTerm, d.dpb[i].longTermIndex = true, operation.Argument2
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("MMCO 3 cannot find short-term picture %d", target)
				}
			case 4:
				maximumPlus1 := operation.Argument1
				filtered := d.dpb[:0]
				for _, picture := range d.dpb {
					if !picture.longTerm || maximumPlus1 != 0 && picture.longTermIndex < maximumPlus1 {
						filtered = append(filtered, picture)
					}
				}
				d.dpb = filtered
			case 5:
				d.dpb = nil
			case 6:
				d.removeLongTerm(operation.Argument1)
				currentLongTerm, currentLongTermIndex = true, operation.Argument1
			}
		}
	} else {
		maximum := int(header.SPS.MaxReferenceFrames)
		if maximum < 1 {
			maximum = 1
		}
		for len(d.dpb) >= maximum {
			if !d.removeOldestShortTerm() {
				return fmt.Errorf("DPB is full of long-term pictures")
			}
		}
	}
	d.dpb = append(d.dpb, referencePicture{
		frame: frame, frameNumber: header.FrameNumber, longTerm: currentLongTerm, longTermIndex: currentLongTermIndex,
		poc: int64(header.PictureOrderCountLSB),
	})
	frame.longTerm = currentLongTerm
	return nil
}

func (d *Decoder) removeShortTerm(frameNumber uint64) {
	for i := range d.dpb {
		if !d.dpb[i].longTerm && d.dpb[i].frameNumber == frameNumber {
			d.dpb = append(d.dpb[:i], d.dpb[i+1:]...)
			return
		}
	}
}

func (d *Decoder) removeLongTerm(index uint64) {
	for i := range d.dpb {
		if d.dpb[i].longTerm && d.dpb[i].longTermIndex == index {
			d.dpb = append(d.dpb[:i], d.dpb[i+1:]...)
			return
		}
	}
}

func (d *Decoder) removeOldestShortTerm() bool {
	for i := range d.dpb {
		if !d.dpb[i].longTerm {
			d.dpb = append(d.dpb[:i], d.dpb[i+1:]...)
			return true
		}
	}
	return false
}
