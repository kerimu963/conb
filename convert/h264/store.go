package h264

import "fmt"

// ParameterSetStore resolves the parameter-set IDs referenced by slices.
type ParameterSetStore struct {
	sequences map[uint64]SPS
	pictures  map[uint64]PPS
}

func NewParameterSetStore() *ParameterSetStore {
	return &ParameterSetStore{sequences: make(map[uint64]SPS), pictures: make(map[uint64]PPS)}
}

// StoreFromConfig parses and stores all SPS/PPS units from avcC.
func StoreFromConfig(config AVCConfig) (*ParameterSetStore, error) {
	store := NewParameterSetStore()
	for _, data := range config.SequenceHeaders {
		unit, err := ParseNALUnit(data)
		if err != nil {
			return nil, err
		}
		if _, err = store.AddSPS(unit); err != nil {
			return nil, err
		}
	}
	for _, data := range config.PictureHeaders {
		unit, err := ParseNALUnit(data)
		if err != nil {
			return nil, err
		}
		if _, err = store.AddPPS(unit); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (s *ParameterSetStore) AddSPS(unit NALUnit) (SPS, error) {
	parsed, err := ParseSPS(unit)
	if err != nil {
		return SPS{}, err
	}
	if s.sequences == nil {
		s.sequences = make(map[uint64]SPS)
	}
	s.sequences[parsed.ID] = parsed
	return parsed, nil
}

func (s *ParameterSetStore) AddPPS(unit NALUnit) (PPS, error) {
	parsed, err := ParsePPS(unit)
	if err != nil {
		return PPS{}, err
	}
	if _, ok := s.sequences[parsed.SequenceID]; !ok {
		return PPS{}, fmt.Errorf("%w: PPS %d references missing SPS %d", ErrMalformed, parsed.ID, parsed.SequenceID)
	}
	if s.pictures == nil {
		s.pictures = make(map[uint64]PPS)
	}
	s.pictures[parsed.ID] = parsed
	return parsed, nil
}

func (s *ParameterSetStore) SPS(id uint64) (SPS, bool) {
	value, ok := s.sequences[id]
	return value, ok
}
func (s *ParameterSetStore) PPS(id uint64) (PPS, bool) { value, ok := s.pictures[id]; return value, ok }
