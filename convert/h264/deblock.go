package h264

type deblockParameters struct {
	qp, chromaQP            int
	alphaOffset, betaOffset int
	disable                 uint64
	slice                   int
}

type interDeblockInfo struct {
	deblockParameters
	intra         bool
	lumaNonZero   [16]bool
	chromaNonZero [2][4]bool
}

func captureInterDeblock(context *CAVLCBlockContext, mbX, mbY int, parameters deblockParameters, intra bool) interDeblockInfo {
	result := interDeblockInfo{deblockParameters: parameters, intra: intra}
	for block := range 16 {
		x, y, _ := context.blockPosition(mbX, mbY, block)
		result.lumaNonZero[block] = context.totalCoeff[[2]int{x, y}] != 0
	}
	for component := range 2 {
		for block := range 4 {
			x, y, _ := context.chromaBlockPosition(component, mbX, mbY, block)
			result.chromaNonZero[component][block] = context.chromaTotal[component][[2]int{x, y}] != 0
		}
	}
	return result
}

func deblockInterPicture(frame *Frame420, info map[int]interDeblockInfo, motion map[[2]int]motionInfo) {
	deblockInterPictureWithStrength(frame, info, func(mbWidth, px, py, qx, qy int, macroblockEdge bool) int {
		return interLumaStrength(info, motion, mbWidth, px, py, qx, qy, macroblockEdge)
	})
}

type interStrengthFunc func(mbWidth, px, py, qx, qy int, macroblockEdge bool) int

func deblockInterPictureWithStrength(frame *Frame420, info map[int]interDeblockInfo, strength interStrengthFunc) {
	mbWidth, mbHeight := frame.Width/16, frame.Height/16
	for mbY := range mbHeight {
		for mbX := range mbWidth {
			address := mbY*mbWidth + mbX
			current := info[address]
			if current.disable == 1 {
				continue
			}
			for edge := 0; edge < 4; edge++ {
				if edge == 0 && mbX == 0 {
					continue
				}
				if edge == 0 && current.disable == 2 && info[address-1].slice != current.slice {
					continue
				}
				qp := current.qp
				if edge == 0 {
					qp = (qp + info[address-1].qp + 1) / 2
				}
				for segment := 0; segment < 4; segment++ {
					bx, by := mbX*4+edge, mbY*4+segment
					bs := strength(mbWidth, bx-1, by, bx, by, edge == 0)
					if bs == 0 {
						continue
					}
					x, y0 := mbX*16+edge*4, mbY*16+segment*4
					for y := y0; y < y0+4; y++ {
						filterSamples(frame.Y, y*frame.Width+x-1, y*frame.Width+x, 1, qp, current.alphaOffset, current.betaOffset, bs, false)
					}
				}
			}
			for edge := 0; edge < 4; edge++ {
				if edge == 0 && mbY == 0 {
					continue
				}
				if edge == 0 && current.disable == 2 && info[address-mbWidth].slice != current.slice {
					continue
				}
				qp := current.qp
				if edge == 0 {
					qp = (qp + info[address-mbWidth].qp + 1) / 2
				}
				for segment := 0; segment < 4; segment++ {
					bx, by := mbX*4+segment, mbY*4+edge
					bs := strength(mbWidth, bx, by-1, bx, by, edge == 0)
					if bs == 0 {
						continue
					}
					y, x0 := mbY*16+edge*4, mbX*16+segment*4
					for x := x0; x < x0+4; x++ {
						filterSamples(frame.Y, (y-1)*frame.Width+x, y*frame.Width+x, frame.Width, qp, current.alphaOffset, current.betaOffset, bs, false)
					}
				}
			}
			deblockInterChroma(frame.Cb, frame.Width/2, mbX, mbY, address, mbWidth, current, info, strength)
			deblockInterChroma(frame.Cr, frame.Width/2, mbX, mbY, address, mbWidth, current, info, strength)
		}
	}
}

func deblockBPicture(frame *Frame420, info map[int]interDeblockInfo, motion [2]map[[2]int]motionInfo) {
	deblockInterPictureWithStrength(frame, info, func(mbWidth, px, py, qx, qy int, macroblockEdge bool) int {
		return interLumaStrengthB(info, motion, mbWidth, px, py, qx, qy, macroblockEdge)
	})
}

func interLumaStrength(info map[int]interDeblockInfo, motion map[[2]int]motionInfo, mbWidth, px, py, qx, qy int, macroblockEdge bool) int {
	pAddress, pBlock := deblockBlockAddress(mbWidth, px, py)
	qAddress, qBlock := deblockBlockAddress(mbWidth, qx, qy)
	pInfo, qInfo := info[pAddress], info[qAddress]
	if pInfo.intra || qInfo.intra {
		if macroblockEdge {
			return 4
		}
		return 3
	}
	if pInfo.lumaNonZero[pBlock] || qInfo.lumaNonZero[qBlock] {
		return 2
	}
	pMotion, qMotion := motion[[2]int{px, py}], motion[[2]int{qx, qy}]
	differentReference := pMotion.picture != nil || qMotion.picture != nil
	if differentReference {
		differentReference = pMotion.picture != qMotion.picture
	} else {
		differentReference = pMotion.reference != qMotion.reference
	}
	if differentReference || absInt(pMotion.vector[0]-qMotion.vector[0]) >= 4 || absInt(pMotion.vector[1]-qMotion.vector[1]) >= 4 {
		return 1
	}
	return 0
}

func interLumaStrengthB(info map[int]interDeblockInfo, motion [2]map[[2]int]motionInfo, mbWidth, px, py, qx, qy int, macroblockEdge bool) int {
	pAddress, pBlock := deblockBlockAddress(mbWidth, px, py)
	qAddress, qBlock := deblockBlockAddress(mbWidth, qx, qy)
	pInfo, qInfo := info[pAddress], info[qAddress]
	if pInfo.intra || qInfo.intra {
		if macroblockEdge {
			return 4
		}
		return 3
	}
	if pInfo.lumaNonZero[pBlock] || qInfo.lumaNonZero[qBlock] {
		return 2
	}
	p0, pHas0 := motion[0][[2]int{px, py}]
	p1, pHas1 := motion[1][[2]int{px, py}]
	q0, qHas0 := motion[0][[2]int{qx, qy}]
	q1, qHas1 := motion[1][[2]int{qx, qy}]
	sameOrder := sameMotionReference(p0, pHas0, q0, qHas0) && sameMotionReference(p1, pHas1, q1, qHas1)
	swappedOrder := sameMotionReference(p0, pHas0, q1, qHas1) && sameMotionReference(p1, pHas1, q0, qHas0)
	if sameOrder && !motionDifferenceAtLeastQuarter(p0, pHas0, q0, qHas0) && !motionDifferenceAtLeastQuarter(p1, pHas1, q1, qHas1) {
		return 0
	}
	if swappedOrder && !motionDifferenceAtLeastQuarter(p0, pHas0, q1, qHas1) && !motionDifferenceAtLeastQuarter(p1, pHas1, q0, qHas0) {
		return 0
	}
	return 1
}

func sameMotionReference(a motionInfo, hasA bool, b motionInfo, hasB bool) bool {
	if hasA != hasB {
		return false
	}
	if !hasA {
		return true
	}
	if a.picture != nil || b.picture != nil {
		return a.picture == b.picture
	}
	return a.reference == b.reference
}

func motionDifferenceAtLeastQuarter(a motionInfo, hasA bool, b motionInfo, hasB bool) bool {
	if !hasA || !hasB {
		return false
	}
	return absInt(a.vector[0]-b.vector[0]) >= 4 || absInt(a.vector[1]-b.vector[1]) >= 4
}

func deblockBlockAddress(mbWidth, blockX, blockY int) (address, block int) {
	mbX, mbY := blockX/4, blockY/4
	localX, localY := blockX%4, blockY%4
	if localX < 0 {
		mbX--
		localX += 4
	}
	if localY < 0 {
		mbY--
		localY += 4
	}
	address = mbY*mbWidth + mbX
	for candidate := range 16 {
		x, y := lumaBlockXY(candidate)
		if x == localX && y == localY {
			return address, candidate
		}
	}
	return address, 0
}

func deblockInterChroma(plane []uint8, width, mbX, mbY, address, mbWidth int, current interDeblockInfo, info map[int]interDeblockInfo, strength interStrengthFunc) {
	for edge := 0; edge < 2; edge++ {
		if edge == 0 && mbX == 0 || edge == 0 && current.disable == 2 && info[address-1].slice != current.slice {
			continue
		}
		qp := current.chromaQP
		if edge == 0 {
			qp = (qp + info[address-1].chromaQP + 1) / 2
		}
		for segment := 0; segment < 2; segment++ {
			lumaEdge, lumaSegment := edge*2, segment*2
			bs := 0
			for offset := range 2 {
				candidate := strength(mbWidth, mbX*4+lumaEdge-1, mbY*4+lumaSegment+offset, mbX*4+lumaEdge, mbY*4+lumaSegment+offset, edge == 0)
				if candidate > bs {
					bs = candidate
				}
			}
			if bs == 0 {
				continue
			}
			x, y0 := mbX*8+edge*4, mbY*8+segment*4
			for y := y0; y < y0+4; y++ {
				filterSamples(plane, y*width+x-1, y*width+x, 1, qp, current.alphaOffset, current.betaOffset, bs, true)
			}
		}
	}
	for edge := 0; edge < 2; edge++ {
		if edge == 0 && mbY == 0 || edge == 0 && current.disable == 2 && info[address-mbWidth].slice != current.slice {
			continue
		}
		qp := current.chromaQP
		if edge == 0 {
			qp = (qp + info[address-mbWidth].chromaQP + 1) / 2
		}
		for segment := 0; segment < 2; segment++ {
			lumaEdge, lumaSegment := edge*2, segment*2
			bs := 0
			for offset := range 2 {
				candidate := strength(mbWidth, mbX*4+lumaSegment+offset, mbY*4+lumaEdge-1, mbX*4+lumaSegment+offset, mbY*4+lumaEdge, edge == 0)
				if candidate > bs {
					bs = candidate
				}
			}
			if bs == 0 {
				continue
			}
			y, x0 := mbY*8+edge*4, mbX*8+segment*4
			for x := x0; x < x0+4; x++ {
				filterSamples(plane, (y-1)*width+x, y*width+x, width, qp, current.alphaOffset, current.betaOffset, bs, true)
			}
		}
	}
}

var deblockAlpha = [52]int{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	4, 4, 5, 6, 7, 8, 9, 10, 12, 13, 15, 17, 20, 22, 25, 28,
	32, 36, 40, 45, 50, 56, 63, 71, 80, 90, 101, 113, 127, 144, 162, 182,
	203, 226, 255, 255,
}

var deblockBeta = [52]int{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 6, 6, 7, 7, 8, 8,
	9, 9, 10, 10, 11, 11, 12, 12, 13, 13, 14, 14, 15, 15, 16, 16,
	17, 17, 18, 18,
}

var deblockTC0 = [3][52]int{
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 4, 4, 4, 5, 6, 6, 7, 8, 9, 10, 11, 13},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 4, 4, 5, 5, 6, 7, 8, 8, 10, 11, 12, 13, 15, 17},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 4, 4, 4, 5, 6, 6, 7, 8, 9, 10, 11, 13, 14, 16, 18, 20, 23, 25},
}

// deblockIntraPicture applies the normative in-loop filter strengths for an
// all-intra picture: 4 at macroblock boundaries and 3 at internal edges.
func deblockIntraPicture(frame *Frame420, parameters map[int]deblockParameters) {
	mbWidth, mbHeight := frame.Width/16, frame.Height/16
	for mbY := range mbHeight {
		for mbX := range mbWidth {
			address := mbY*mbWidth + mbX
			current := parameters[address]
			if current.disable == 1 {
				continue
			}
			for edge := 0; edge < 4; edge++ {
				if edge == 0 && mbX == 0 {
					continue
				}
				qp, bs := current.qp, 3
				if edge == 0 {
					left := parameters[address-1]
					if current.disable == 2 && left.slice != current.slice {
						continue
					}
					qp, bs = (current.qp+left.qp+1)/2, 4
				}
				x := mbX*16 + edge*4
				for y := mbY * 16; y < mbY*16+16; y++ {
					filterSamples(frame.Y, y*frame.Width+x-1, y*frame.Width+x, 1, qp, current.alphaOffset, current.betaOffset, bs, false)
				}
			}
			for edge := 0; edge < 4; edge++ {
				if edge == 0 && mbY == 0 {
					continue
				}
				qp, bs := current.qp, 3
				if edge == 0 {
					top := parameters[address-mbWidth]
					if current.disable == 2 && top.slice != current.slice {
						continue
					}
					qp, bs = (current.qp+top.qp+1)/2, 4
				}
				y := mbY*16 + edge*4
				for x := mbX * 16; x < mbX*16+16; x++ {
					filterSamples(frame.Y, (y-1)*frame.Width+x, y*frame.Width+x, frame.Width, qp, current.alphaOffset, current.betaOffset, bs, false)
				}
			}
			deblockIntraChroma(frame.Cb, frame.Width/2, mbX, mbY, address, mbWidth, current, parameters)
			deblockIntraChroma(frame.Cr, frame.Width/2, mbX, mbY, address, mbWidth, current, parameters)
		}
	}
}

func deblockIntraChroma(plane []uint8, width, mbX, mbY, address, mbWidth int, current deblockParameters, all map[int]deblockParameters) {
	for edge := 0; edge < 2; edge++ {
		if edge == 0 && mbX == 0 {
			continue
		}
		qp, bs := current.chromaQP, 3
		if edge == 0 {
			left := all[address-1]
			if current.disable == 2 && left.slice != current.slice {
				continue
			}
			qp, bs = (current.chromaQP+left.chromaQP+1)/2, 4
		}
		x := mbX*8 + edge*4
		for y := mbY * 8; y < mbY*8+8; y++ {
			filterSamples(plane, y*width+x-1, y*width+x, 1, qp, current.alphaOffset, current.betaOffset, bs, true)
		}
	}
	for edge := 0; edge < 2; edge++ {
		if edge == 0 && mbY == 0 {
			continue
		}
		qp, bs := current.chromaQP, 3
		if edge == 0 {
			top := all[address-mbWidth]
			if current.disable == 2 && top.slice != current.slice {
				continue
			}
			qp, bs = (current.chromaQP+top.chromaQP+1)/2, 4
		}
		y := mbY*8 + edge*4
		for x := mbX * 8; x < mbX*8+8; x++ {
			filterSamples(plane, (y-1)*width+x, y*width+x, width, qp, current.alphaOffset, current.betaOffset, bs, true)
		}
	}
}

func filterSamples(plane []uint8, p0Index, q0Index, step, qp, alphaOffset, betaOffset, bs int, chroma bool) {
	indexA, indexB := clamp(qp+alphaOffset, 0, 51), clamp(qp+betaOffset, 0, 51)
	alpha, beta := deblockAlpha[indexA], deblockBeta[indexB]
	p0, p1, p2 := int(plane[p0Index]), int(plane[p0Index-step]), int(plane[p0Index-2*step])
	q0, q1, q2 := int(plane[q0Index]), int(plane[q0Index+step]), int(plane[q0Index+2*step])
	if absInt(p0-q0) >= alpha || absInt(p1-p0) >= beta || absInt(q1-q0) >= beta {
		return
	}
	if bs < 4 {
		tc0 := deblockTC0[bs-1][indexA]
		ap, aq := absInt(p2-p0), absInt(q2-q0)
		tc := tc0 + 1
		if !chroma {
			tc = tc0
			if ap < beta {
				tc++
				plane[p0Index-step] = clipByte(int64(p1 + clipInt((p2+((p0+q0+1)>>1)-(p1<<1))>>1, -tc0, tc0)))
			}
			if aq < beta {
				tc++
				plane[q0Index+step] = clipByte(int64(q1 + clipInt((q2+((p0+q0+1)>>1)-(q1<<1))>>1, -tc0, tc0)))
			}
		}
		delta := clipInt((((q0-p0)<<2)+(p1-q1)+4)>>3, -tc, tc)
		plane[p0Index], plane[q0Index] = clipByte(int64(p0+delta)), clipByte(int64(q0-delta))
		return
	}
	if !chroma && absInt(p2-p0) < beta && absInt(p0-q0) < (alpha>>2)+2 {
		p3 := int(plane[p0Index-3*step])
		plane[p0Index] = uint8((p2 + 2*p1 + 2*p0 + 2*q0 + q1 + 4) >> 3)
		plane[p0Index-step] = uint8((p2 + p1 + p0 + q0 + 2) >> 2)
		plane[p0Index-2*step] = uint8((2*p3 + 3*p2 + p1 + p0 + q0 + 4) >> 3)
	} else {
		plane[p0Index] = uint8((2*p1 + p0 + q1 + 2) >> 2)
	}
	if !chroma && absInt(q2-q0) < beta && absInt(p0-q0) < (alpha>>2)+2 {
		q3 := int(plane[q0Index+3*step])
		plane[q0Index] = uint8((p1 + 2*p0 + 2*q0 + 2*q1 + q2 + 4) >> 3)
		plane[q0Index+step] = uint8((p0 + q0 + q1 + q2 + 2) >> 2)
		plane[q0Index+2*step] = uint8((2*q3 + 3*q2 + q1 + q0 + p0 + 4) >> 3)
	} else {
		plane[q0Index] = uint8((2*q1 + q0 + p1 + 2) >> 2)
	}
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func clipInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
