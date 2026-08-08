package h264

import "fmt"

type SliceType uint8

const (
	SliceP SliceType = iota
	SliceB
	SliceI
	SliceSP
	SliceSI
)

func (s SliceType) String() string {
	if s > SliceSI {
		return fmt.Sprintf("SliceType(%d)", s)
	}
	return [...]string{"P", "B", "I", "SP", "SI"}[s]
}

// ReferenceModification is one ref_pic_list_modification operation.
type ReferenceModification struct {
	List  uint8
	IDC   uint64
	Value uint64
}

// PredictionWeight contains one reference picture's prediction coefficients.
type PredictionWeight struct {
	LumaPresent   bool
	LumaWeight    int64
	LumaOffset    int64
	ChromaPresent bool
	ChromaWeight  [2]int64
	ChromaOffset  [2]int64
}

// MemoryManagementControl contains one adaptive reference-picture operation.
type MemoryManagementControl struct {
	Operation uint64
	Argument1 uint64
	Argument2 uint64
}

// SliceHeader contains slice_header syntax shared by entropy decoding modes.
// HeaderBits is the start of slice_data in the unescaped RBSP.
type SliceHeader struct {
	FirstMacroblock          uint64
	Type                     SliceType
	AllSlicesSameType        bool
	PictureParameterSetID    uint64
	ColourPlaneID            uint8
	FrameNumber              uint64
	FieldPicture             bool
	BottomField              bool
	IDRPictureID             uint64
	PictureOrderCountLSB     uint64
	DeltaPictureOrderBottom  int64
	DeltaPictureOrder        [2]int64
	RedundantPictureCount    uint64
	DirectSpatialPrediction  bool
	ReferenceCount           [2]uint64
	ReferenceOverride        bool
	ReferenceModifications   []ReferenceModification
	LumaLog2WeightDenom      uint64
	ChromaLog2WeightDenom    uint64
	PredictionWeights        [2][]PredictionWeight
	NoOutputOfPriorPictures  bool
	LongTermReference        bool
	AdaptiveReferenceMarking bool
	MemoryManagement         []MemoryManagementControl
	CABACInitIDC             uint64
	SliceQPDelta             int64
	SliceQP                  int64
	SPForSwitch              bool
	SliceQSDelta             int64
	SliceQS                  int64
	DisableDeblockingFilter  uint64
	SliceAlphaOffset         int64
	SliceBetaOffset          int64
	HeaderBits               int
	SPS                      SPS
	PPS                      PPS
}

// ParseSliceHeader resolves the PPS/SPS and parses the core header of a
// non-partitioned coded slice NAL unit.
func ParseSliceHeader(unit NALUnit, store *ParameterSetStore) (SliceHeader, error) {
	if unit.Type != NALSliceNonIDR && unit.Type != NALSliceIDR {
		return SliceHeader{}, fmt.Errorf("%w: NAL type %d is not a supported coded slice", ErrMalformed, unit.Type)
	}
	if store == nil {
		return SliceHeader{}, fmt.Errorf("%w: nil parameter-set store", ErrMalformed)
	}
	r, err := NewBitReader(unit.Payload())
	if err != nil {
		return SliceHeader{}, err
	}
	var header SliceHeader
	if header.FirstMacroblock, err = r.ReadUE(); err != nil {
		return SliceHeader{}, malformed("slice first_mb_in_slice is truncated")
	}
	rawType, err := r.ReadUE()
	if err != nil || rawType > 9 {
		return SliceHeader{}, malformed("invalid slice_type")
	}
	header.Type = SliceType(rawType % 5)
	header.AllSlicesSameType = rawType >= 5
	if header.PictureParameterSetID, err = r.ReadUE(); err != nil {
		return SliceHeader{}, malformed("slice pic_parameter_set_id is truncated")
	}
	pps, ok := store.PPS(header.PictureParameterSetID)
	if !ok {
		return SliceHeader{}, fmt.Errorf("%w: slice references missing PPS %d", ErrMalformed, header.PictureParameterSetID)
	}
	sps, ok := store.SPS(pps.SequenceID)
	if !ok {
		return SliceHeader{}, fmt.Errorf("%w: PPS references missing SPS %d", ErrMalformed, pps.SequenceID)
	}
	header.PPS, header.SPS = pps, sps
	if pps.NumSliceGroups > 1 {
		return SliceHeader{}, fmt.Errorf("%w: multiple slice groups are not implemented", ErrMalformed)
	}
	if sps.SeparateColourPlane {
		value, readErr := r.ReadBits(2)
		if readErr != nil {
			return SliceHeader{}, malformed("slice colour_plane_id is truncated")
		}
		header.ColourPlaneID = uint8(value)
	}
	if header.FrameNumber, err = r.ReadBits(uint(sps.Log2MaxFrameNum)); err != nil {
		return SliceHeader{}, malformed("slice frame_num is truncated")
	}
	if !sps.FrameMbsOnly {
		bit, readErr := r.ReadBit()
		if readErr != nil {
			return SliceHeader{}, malformed("slice field_pic_flag is truncated")
		}
		header.FieldPicture = bit != 0
		if header.FieldPicture {
			bit, readErr = r.ReadBit()
			if readErr != nil {
				return SliceHeader{}, malformed("slice bottom_field_flag is truncated")
			}
			header.BottomField = bit != 0
		}
	}
	if unit.Type == NALSliceIDR {
		if header.IDRPictureID, err = r.ReadUE(); err != nil {
			return SliceHeader{}, malformed("slice idr_pic_id is truncated")
		}
	}
	if sps.PictureOrderCountType == 0 {
		if header.PictureOrderCountLSB, err = r.ReadBits(uint(sps.Log2MaxPictureOrderCount)); err != nil {
			return SliceHeader{}, malformed("slice pic_order_cnt_lsb is truncated")
		}
		if pps.BottomFieldOrderInFrame && !header.FieldPicture {
			if header.DeltaPictureOrderBottom, err = r.ReadSE(); err != nil {
				return SliceHeader{}, malformed("slice delta_pic_order_cnt_bottom is truncated")
			}
		}
	} else if sps.PictureOrderCountType == 1 && !sps.DeltaPictureOrderAlwaysZero {
		if header.DeltaPictureOrder[0], err = r.ReadSE(); err != nil {
			return SliceHeader{}, malformed("slice delta_pic_order_cnt[0] is truncated")
		}
		if pps.BottomFieldOrderInFrame && !header.FieldPicture {
			if header.DeltaPictureOrder[1], err = r.ReadSE(); err != nil {
				return SliceHeader{}, malformed("slice delta_pic_order_cnt[1] is truncated")
			}
		}
	}
	if pps.RedundantPictureCount {
		if header.RedundantPictureCount, err = r.ReadUE(); err != nil {
			return SliceHeader{}, malformed("slice redundant_pic_cnt is truncated")
		}
	}
	if header.Type == SliceB {
		bit, readErr := r.ReadBit()
		if readErr != nil {
			return SliceHeader{}, malformed("slice direct_spatial_mv_pred_flag is truncated")
		}
		header.DirectSpatialPrediction = bit != 0
	}
	header.ReferenceCount = [2]uint64{pps.DefaultReferenceL0, pps.DefaultReferenceL1}
	if header.Type == SliceP || header.Type == SliceSP || header.Type == SliceB {
		bit, readErr := r.ReadBit()
		if readErr != nil {
			return SliceHeader{}, malformed("slice num_ref_idx_active_override_flag is truncated")
		}
		header.ReferenceOverride = bit != 0
		if header.ReferenceOverride {
			minus1, readErr := r.ReadUE()
			if readErr != nil || minus1 > 31 {
				return SliceHeader{}, malformed("invalid num_ref_idx_l0_active_minus1")
			}
			header.ReferenceCount[0] = minus1 + 1
			if header.Type == SliceB {
				minus1, readErr = r.ReadUE()
				if readErr != nil || minus1 > 31 {
					return SliceHeader{}, malformed("invalid num_ref_idx_l1_active_minus1")
				}
				header.ReferenceCount[1] = minus1 + 1
			}
		}
	}
	if err := parseReferenceModifications(r, &header); err != nil {
		return SliceHeader{}, err
	}
	weighted := (pps.WeightedPrediction && (header.Type == SliceP || header.Type == SliceSP)) ||
		(pps.WeightedBipredIDC == 1 && header.Type == SliceB)
	if weighted {
		if err := parsePredictionWeights(r, &header); err != nil {
			return SliceHeader{}, err
		}
	}
	if unit.RefIDC != 0 {
		if err := parseReferenceMarking(r, unit.Type == NALSliceIDR, &header); err != nil {
			return SliceHeader{}, err
		}
	}
	if pps.EntropyCodingCABAC && header.Type != SliceI && header.Type != SliceSI {
		if header.CABACInitIDC, err = r.ReadUE(); err != nil || header.CABACInitIDC > 2 {
			return SliceHeader{}, malformed("invalid cabac_init_idc")
		}
	}
	if header.SliceQPDelta, err = r.ReadSE(); err != nil {
		return SliceHeader{}, malformed("slice_qp_delta is truncated")
	}
	header.SliceQP = pps.InitialQP + header.SliceQPDelta
	qpOffset := int64(6 * (sps.BitDepthLuma - 8))
	if header.SliceQP < -qpOffset || header.SliceQP > 51 {
		return SliceHeader{}, malformed("slice QP is out of range")
	}
	if header.Type == SliceSP || header.Type == SliceSI {
		if header.Type == SliceSP {
			bit, readErr := r.ReadBit()
			if readErr != nil {
				return SliceHeader{}, malformed("sp_for_switch_flag is truncated")
			}
			header.SPForSwitch = bit != 0
		}
		if header.SliceQSDelta, err = r.ReadSE(); err != nil {
			return SliceHeader{}, malformed("slice_qs_delta is truncated")
		}
		header.SliceQS = pps.InitialQS + header.SliceQSDelta
		if header.SliceQS < 0 || header.SliceQS > 51 {
			return SliceHeader{}, malformed("slice QS is out of range")
		}
	}
	if pps.DeblockingFilterControl {
		if header.DisableDeblockingFilter, err = r.ReadUE(); err != nil || header.DisableDeblockingFilter > 2 {
			return SliceHeader{}, malformed("invalid disable_deblocking_filter_idc")
		}
		if header.DisableDeblockingFilter != 1 {
			alphaDiv2, readErr := r.ReadSE()
			if readErr != nil || alphaDiv2 < -6 || alphaDiv2 > 6 {
				return SliceHeader{}, malformed("invalid slice_alpha_c0_offset_div2")
			}
			betaDiv2, readErr := r.ReadSE()
			if readErr != nil || betaDiv2 < -6 || betaDiv2 > 6 {
				return SliceHeader{}, malformed("invalid slice_beta_offset_div2")
			}
			header.SliceAlphaOffset, header.SliceBetaOffset = alphaDiv2*2, betaDiv2*2
		}
	}
	header.HeaderBits = r.Position()
	return header, nil
}

// SliceDataReader returns an RBSP reader positioned at slice_data.
func SliceDataReader(unit NALUnit, header SliceHeader) (*BitReader, error) {
	r, err := NewBitReader(unit.Payload())
	if err != nil {
		return nil, err
	}
	if err := r.SkipBits(header.HeaderBits); err != nil {
		return nil, malformed("slice header offset exceeds RBSP")
	}
	return r, nil
}

func parseReferenceModifications(r *BitReader, h *SliceHeader) error {
	if h.Type == SliceI || h.Type == SliceSI {
		return nil
	}
	lists := 1
	if h.Type == SliceB {
		lists = 2
	}
	for list := 0; list < lists; list++ {
		flag, err := r.ReadBit()
		if err != nil {
			return malformed("ref_pic_list_modification_flag is truncated")
		}
		if flag == 0 {
			continue
		}
		for operations := 0; ; operations++ {
			if operations > 1<<20 {
				return malformed("too many reference-list modifications")
			}
			idc, err := r.ReadUE()
			if err != nil || idc > 3 {
				return malformed("invalid modification_of_pic_nums_idc")
			}
			if idc == 3 {
				break
			}
			value, err := r.ReadUE()
			if err != nil {
				return malformed("reference-list modification value is truncated")
			}
			h.ReferenceModifications = append(h.ReferenceModifications, ReferenceModification{List: uint8(list), IDC: idc, Value: value})
		}
	}
	return nil
}

func parsePredictionWeights(r *BitReader, h *SliceHeader) error {
	var err error
	if h.LumaLog2WeightDenom, err = r.ReadUE(); err != nil || h.LumaLog2WeightDenom > 7 {
		return malformed("invalid luma_log2_weight_denom")
	}
	chromaPresent := h.SPS.ChromaFormat != 0 && !h.SPS.SeparateColourPlane
	if chromaPresent {
		if h.ChromaLog2WeightDenom, err = r.ReadUE(); err != nil || h.ChromaLog2WeightDenom > 7 {
			return malformed("invalid chroma_log2_weight_denom")
		}
	}
	lists := 1
	if h.Type == SliceB {
		lists = 2
	}
	for list := 0; list < lists; list++ {
		count := h.ReferenceCount[list]
		h.PredictionWeights[list] = make([]PredictionWeight, count)
		for i := range h.PredictionWeights[list] {
			weight := &h.PredictionWeights[list][i]
			weight.LumaWeight = int64(uint64(1) << h.LumaLog2WeightDenom)
			flag, readErr := r.ReadBit()
			if readErr != nil {
				return malformed("luma_weight_flag is truncated")
			}
			if flag != 0 {
				weight.LumaPresent = true
				if weight.LumaWeight, readErr = r.ReadSE(); readErr != nil {
					return malformed("luma_weight is truncated")
				}
				if weight.LumaOffset, readErr = r.ReadSE(); readErr != nil {
					return malformed("luma_offset is truncated")
				}
			}
			if chromaPresent {
				defaultWeight := int64(uint64(1) << h.ChromaLog2WeightDenom)
				weight.ChromaWeight = [2]int64{defaultWeight, defaultWeight}
				flag, readErr = r.ReadBit()
				if readErr != nil {
					return malformed("chroma_weight_flag is truncated")
				}
				if flag != 0 {
					weight.ChromaPresent = true
					for component := range 2 {
						if weight.ChromaWeight[component], readErr = r.ReadSE(); readErr != nil {
							return malformed("chroma_weight is truncated")
						}
						if weight.ChromaOffset[component], readErr = r.ReadSE(); readErr != nil {
							return malformed("chroma_offset is truncated")
						}
					}
				}
			}
		}
	}
	return nil
}

func parseReferenceMarking(r *BitReader, idr bool, h *SliceHeader) error {
	if idr {
		noOutput, err := r.ReadBit()
		if err != nil {
			return malformed("no_output_of_prior_pics_flag is truncated")
		}
		longTerm, err := r.ReadBit()
		if err != nil {
			return malformed("long_term_reference_flag is truncated")
		}
		h.NoOutputOfPriorPictures, h.LongTermReference = noOutput != 0, longTerm != 0
		return nil
	}
	adaptive, err := r.ReadBit()
	if err != nil {
		return malformed("adaptive_ref_pic_marking_mode_flag is truncated")
	}
	h.AdaptiveReferenceMarking = adaptive != 0
	if !h.AdaptiveReferenceMarking {
		return nil
	}
	for operations := 0; ; operations++ {
		if operations > 1<<20 {
			return malformed("too many memory-management operations")
		}
		op, err := r.ReadUE()
		if err != nil || op > 6 {
			return malformed("invalid memory_management_control_operation")
		}
		if op == 0 {
			return nil
		}
		item := MemoryManagementControl{Operation: op}
		if op == 1 || op == 3 {
			item.Argument1, err = r.ReadUE()
		} else if op == 2 {
			item.Argument1, err = r.ReadUE()
		} else if op == 4 {
			item.Argument1, err = r.ReadUE()
		} else if op == 6 {
			item.Argument1, err = r.ReadUE()
		}
		if err != nil {
			return malformed("memory-management argument is truncated")
		}
		if op == 3 {
			item.Argument2, err = r.ReadUE()
			if err != nil {
				return malformed("memory-management long-term index is truncated")
			}
		}
		h.MemoryManagement = append(h.MemoryManagement, item)
	}
}
