package h264

import "fmt"

// SPS contains sequence-level values needed to size and begin decoding frames.
type SPS struct {
	ID                          uint64
	Profile, Compatibility      uint8
	Level                       uint8
	ChromaFormat                uint64
	SeparateColourPlane         bool
	BitDepthLuma                uint64
	BitDepthChroma              uint64
	Log2MaxFrameNum             uint64
	PictureOrderCountType       uint64
	Log2MaxPictureOrderCount    uint64
	DeltaPictureOrderAlwaysZero bool
	MaxReferenceFrames          uint64
	FrameMbsOnly                bool
	Direct8x8Inference          bool
	CodedWidth, CodedHeight     uint64
	CropLeft, CropRight         uint64
	CropTop, CropBottom         uint64
	Width, Height               uint64
}

// PPS contains the first picture-parameter values used to select an SPS and
// entropy decoder. More slice-related fields will be added with slice decoding.
type PPS struct {
	ID                      uint64
	SequenceID              uint64
	EntropyCodingCABAC      bool
	BottomFieldOrderInFrame bool
	NumSliceGroups          uint64
	DefaultReferenceL0      uint64
	DefaultReferenceL1      uint64
	WeightedPrediction      bool
	WeightedBipredIDC       uint8
	InitialQP               int64
	InitialQS               int64
	ChromaQPIndexOffset     int64
	DeblockingFilterControl bool
	ConstrainedIntra        bool
	RedundantPictureCount   bool
}

// ParseSPS parses a type-7 NAL unit.
func ParseSPS(unit NALUnit) (SPS, error) {
	if unit.Type != NALSPS {
		return SPS{}, fmt.Errorf("%w: expected SPS NAL, got type %d", ErrMalformed, unit.Type)
	}
	r, err := NewBitReader(unit.Payload())
	if err != nil {
		return SPS{}, err
	}
	profile, err := read8(r)
	if err != nil {
		return SPS{}, malformed("SPS profile is truncated")
	}
	compatibility, err := read8(r)
	if err != nil {
		return SPS{}, malformed("SPS compatibility is truncated")
	}
	level, err := read8(r)
	if err != nil {
		return SPS{}, malformed("SPS level is truncated")
	}
	id, err := r.ReadUE()
	if err != nil {
		return SPS{}, malformed("SPS id is truncated")
	}
	sps := SPS{
		ID: id, Profile: profile, Compatibility: compatibility, Level: level,
		ChromaFormat: 1, BitDepthLuma: 8, BitDepthChroma: 8,
	}
	if isHighProfile(profile) {
		if sps.ChromaFormat, err = r.ReadUE(); err != nil || sps.ChromaFormat > 3 {
			return SPS{}, malformed("invalid SPS chroma_format_idc")
		}
		if sps.ChromaFormat == 3 {
			bit, readErr := r.ReadBit()
			if readErr != nil {
				return SPS{}, malformed("SPS separate_colour_plane_flag is truncated")
			}
			sps.SeparateColourPlane = bit != 0
		}
		lumaMinus8, readErr := r.ReadUE()
		if readErr != nil || lumaMinus8 > 6 {
			return SPS{}, malformed("invalid SPS bit_depth_luma_minus8")
		}
		chromaMinus8, readErr := r.ReadUE()
		if readErr != nil || chromaMinus8 > 6 {
			return SPS{}, malformed("invalid SPS bit_depth_chroma_minus8")
		}
		sps.BitDepthLuma, sps.BitDepthChroma = lumaMinus8+8, chromaMinus8+8
		if _, err = r.ReadBit(); err != nil { // qpprime_y_zero_transform_bypass_flag
			return SPS{}, malformed("SPS transform bypass flag is truncated")
		}
		scaling, readErr := r.ReadBit()
		if readErr != nil {
			return SPS{}, malformed("SPS scaling matrix flag is truncated")
		}
		if scaling != 0 {
			count := 8
			if sps.ChromaFormat == 3 {
				count = 12
			}
			for i := 0; i < count; i++ {
				present, readErr := r.ReadBit()
				if readErr != nil {
					return SPS{}, malformed("SPS scaling list flag is truncated")
				}
				if present != 0 {
					size := 16
					if i >= 6 {
						size = 64
					}
					if err := skipScalingList(r, size); err != nil {
						return SPS{}, err
					}
				}
			}
		}
	}

	frameMinus4, err := r.ReadUE()
	if err != nil || frameMinus4 > 12 {
		return SPS{}, malformed("invalid log2_max_frame_num_minus4")
	}
	sps.Log2MaxFrameNum = frameMinus4 + 4
	if sps.PictureOrderCountType, err = r.ReadUE(); err != nil || sps.PictureOrderCountType > 2 {
		return SPS{}, malformed("invalid pic_order_cnt_type")
	}
	if sps.PictureOrderCountType == 0 {
		value, readErr := r.ReadUE()
		if readErr != nil || value > 12 {
			return SPS{}, malformed("invalid log2_max_pic_order_cnt_lsb_minus4")
		}
		sps.Log2MaxPictureOrderCount = value + 4
	} else if sps.PictureOrderCountType == 1 {
		alwaysZero, readErr := r.ReadBit()
		if readErr != nil {
			return SPS{}, malformed("SPS delta_pic_order_always_zero_flag is truncated")
		}
		sps.DeltaPictureOrderAlwaysZero = alwaysZero != 0
		if _, err = r.ReadSE(); err != nil {
			return SPS{}, malformed("SPS offset_for_non_ref_pic is truncated")
		}
		if _, err = r.ReadSE(); err != nil {
			return SPS{}, malformed("SPS offset_for_top_to_bottom_field is truncated")
		}
		cycle, readErr := r.ReadUE()
		if readErr != nil || cycle > 255 {
			return SPS{}, malformed("invalid num_ref_frames_in_pic_order_cnt_cycle")
		}
		for range cycle {
			if _, err = r.ReadSE(); err != nil {
				return SPS{}, malformed("SPS reference-frame offset is truncated")
			}
		}
	}
	if sps.MaxReferenceFrames, err = r.ReadUE(); err != nil {
		return SPS{}, malformed("SPS max_num_ref_frames is truncated")
	}
	if _, err = r.ReadBit(); err != nil { // gaps_in_frame_num_value_allowed_flag
		return SPS{}, malformed("SPS frame number gap flag is truncated")
	}
	widthMbs, err := r.ReadUE()
	if err != nil {
		return SPS{}, malformed("SPS width is truncated")
	}
	heightMapUnits, err := r.ReadUE()
	if err != nil {
		return SPS{}, malformed("SPS height is truncated")
	}
	frameOnly, err := r.ReadBit()
	if err != nil {
		return SPS{}, malformed("SPS frame_mbs_only_flag is truncated")
	}
	sps.FrameMbsOnly = frameOnly != 0
	if !sps.FrameMbsOnly {
		if _, err = r.ReadBit(); err != nil {
			return SPS{}, malformed("SPS mb_adaptive_frame_field_flag is truncated")
		}
	}
	directInference, readErr := r.ReadBit()
	if readErr != nil {
		return SPS{}, malformed("SPS direct inference flag is truncated")
	}
	sps.Direct8x8Inference = directInference != 0
	cropping, err := r.ReadBit()
	if err != nil {
		return SPS{}, malformed("SPS cropping flag is truncated")
	}
	if cropping != 0 {
		if sps.CropLeft, err = r.ReadUE(); err != nil {
			return SPS{}, malformed("SPS crop_left is truncated")
		}
		if sps.CropRight, err = r.ReadUE(); err != nil {
			return SPS{}, malformed("SPS crop_right is truncated")
		}
		if sps.CropTop, err = r.ReadUE(); err != nil {
			return SPS{}, malformed("SPS crop_top is truncated")
		}
		if sps.CropBottom, err = r.ReadUE(); err != nil {
			return SPS{}, malformed("SPS crop_bottom is truncated")
		}
	}

	frameFactor := uint64(2)
	if sps.FrameMbsOnly {
		frameFactor = 1
	}
	sps.CodedWidth = (widthMbs + 1) * 16
	sps.CodedHeight = frameFactor * (heightMapUnits + 1) * 16
	cropUnitX, cropUnitY := cropUnits(sps.ChromaFormat, sps.SeparateColourPlane, frameFactor)
	horizontalCrop := (sps.CropLeft + sps.CropRight) * cropUnitX
	verticalCrop := (sps.CropTop + sps.CropBottom) * cropUnitY
	if horizontalCrop >= sps.CodedWidth || verticalCrop >= sps.CodedHeight {
		return SPS{}, malformed("SPS crop exceeds coded dimensions")
	}
	sps.Width = sps.CodedWidth - horizontalCrop
	sps.Height = sps.CodedHeight - verticalCrop
	return sps, nil
}

// ParsePPS parses the initial fields of a type-8 NAL unit.
func ParsePPS(unit NALUnit) (PPS, error) {
	if unit.Type != NALPPS {
		return PPS{}, fmt.Errorf("%w: expected PPS NAL, got type %d", ErrMalformed, unit.Type)
	}
	r, err := NewBitReader(unit.Payload())
	if err != nil {
		return PPS{}, err
	}
	pps := PPS{}
	if pps.ID, err = r.ReadUE(); err != nil {
		return PPS{}, malformed("PPS id is truncated")
	}
	if pps.SequenceID, err = r.ReadUE(); err != nil {
		return PPS{}, malformed("PPS sequence id is truncated")
	}
	bit, err := r.ReadBit()
	if err != nil {
		return PPS{}, malformed("PPS entropy coding flag is truncated")
	}
	pps.EntropyCodingCABAC = bit != 0
	bit, err = r.ReadBit()
	if err != nil {
		return PPS{}, malformed("PPS bottom field order flag is truncated")
	}
	pps.BottomFieldOrderInFrame = bit != 0
	groupsMinus1, err := r.ReadUE()
	if err != nil || groupsMinus1 > 7 {
		return PPS{}, malformed("invalid PPS num_slice_groups_minus1")
	}
	pps.NumSliceGroups = groupsMinus1 + 1
	if groupsMinus1 > 0 {
		if err := skipSliceGroupMap(r, groupsMinus1); err != nil {
			return PPS{}, err
		}
	}
	defaultL0, err := r.ReadUE()
	if err != nil || defaultL0 > 31 {
		return PPS{}, malformed("invalid PPS num_ref_idx_l0_default_active_minus1")
	}
	defaultL1, err := r.ReadUE()
	if err != nil || defaultL1 > 31 {
		return PPS{}, malformed("invalid PPS num_ref_idx_l1_default_active_minus1")
	}
	pps.DefaultReferenceL0, pps.DefaultReferenceL1 = defaultL0+1, defaultL1+1
	bit, err = r.ReadBit()
	if err != nil {
		return PPS{}, malformed("PPS weighted prediction flag is truncated")
	}
	pps.WeightedPrediction = bit != 0
	bipred, err := r.ReadBits(2)
	if err != nil {
		return PPS{}, malformed("PPS weighted_bipred_idc is truncated")
	}
	pps.WeightedBipredIDC = uint8(bipred)
	qpMinus26, err := r.ReadSE()
	if err != nil || qpMinus26 < -26 || qpMinus26 > 25 {
		return PPS{}, malformed("invalid PPS pic_init_qp_minus26")
	}
	pps.InitialQP = qpMinus26 + 26
	qsMinus26, err := r.ReadSE()
	if err != nil || qsMinus26 < -26 || qsMinus26 > 25 {
		return PPS{}, malformed("invalid PPS pic_init_qs_minus26")
	}
	pps.InitialQS = qsMinus26 + 26
	if pps.ChromaQPIndexOffset, err = r.ReadSE(); err != nil || pps.ChromaQPIndexOffset < -12 || pps.ChromaQPIndexOffset > 12 {
		return PPS{}, malformed("invalid PPS chroma_qp_index_offset")
	}
	bit, err = r.ReadBit()
	if err != nil {
		return PPS{}, malformed("PPS deblocking filter flag is truncated")
	}
	pps.DeblockingFilterControl = bit != 0
	bit, err = r.ReadBit()
	if err != nil {
		return PPS{}, malformed("PPS constrained intra flag is truncated")
	}
	pps.ConstrainedIntra = bit != 0
	bit, err = r.ReadBit()
	if err != nil {
		return PPS{}, malformed("PPS redundant picture count flag is truncated")
	}
	pps.RedundantPictureCount = bit != 0
	return pps, nil
}

func skipSliceGroupMap(r *BitReader, groupsMinus1 uint64) error {
	mapType, err := r.ReadUE()
	if err != nil || mapType > 6 {
		return malformed("invalid PPS slice_group_map_type")
	}
	switch mapType {
	case 0:
		for i := uint64(0); i <= groupsMinus1; i++ {
			if _, err = r.ReadUE(); err != nil {
				return malformed("PPS run_length_minus1 is truncated")
			}
		}
	case 2:
		for i := uint64(0); i < groupsMinus1; i++ {
			if _, err = r.ReadUE(); err != nil {
				return malformed("PPS top_left is truncated")
			}
			if _, err = r.ReadUE(); err != nil {
				return malformed("PPS bottom_right is truncated")
			}
		}
	case 3, 4, 5:
		if _, err = r.ReadBit(); err != nil {
			return malformed("PPS slice_group_change_direction_flag is truncated")
		}
		if _, err = r.ReadUE(); err != nil {
			return malformed("PPS slice_group_change_rate_minus1 is truncated")
		}
	case 6:
		pictureSizeMinus1, readErr := r.ReadUE()
		if readErr != nil || pictureSizeMinus1 > 1<<28 {
			return malformed("invalid PPS pic_size_in_map_units_minus1")
		}
		bits := uint(0)
		for (uint64(1) << bits) < groupsMinus1+1 {
			bits++
		}
		for i := uint64(0); i <= pictureSizeMinus1; i++ {
			id, readErr := r.ReadBits(bits)
			if readErr != nil || id > groupsMinus1 {
				return malformed("invalid PPS slice_group_id")
			}
		}
	}
	return nil
}

func read8(r *BitReader) (uint8, error) {
	value, err := r.ReadBits(8)
	return uint8(value), err
}

func isHighProfile(profile uint8) bool {
	switch profile {
	case 44, 83, 86, 100, 110, 118, 122, 128, 134, 135, 138, 139, 144, 244:
		return true
	default:
		return false
	}
}

func skipScalingList(r *BitReader, size int) error {
	lastScale, nextScale := int64(8), int64(8)
	for i := 0; i < size; i++ {
		if nextScale != 0 {
			delta, err := r.ReadSE()
			if err != nil {
				return malformed("SPS scaling list is truncated")
			}
			nextScale = (lastScale + delta + 256) % 256
		}
		if nextScale != 0 {
			lastScale = nextScale
		}
	}
	return nil
}

func cropUnits(chroma uint64, separate bool, frameFactor uint64) (uint64, uint64) {
	if separate || chroma == 0 {
		return 1, frameFactor
	}
	subWidth, subHeight := uint64(1), uint64(1)
	switch chroma {
	case 1:
		subWidth, subHeight = 2, 2
	case 2:
		subWidth = 2
	}
	return subWidth, subHeight * frameFactor
}
