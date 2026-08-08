package h264

import (
	"errors"
	"fmt"
)

// ErrCAVLCContextUnsupported indicates a specialized coefficient context that
// is not available yet (currently the 2x4/4:2:2 chroma-DC nC=-2 table).
var ErrCAVLCContextUnsupported = errors.New("CAVLC coefficient context is not implemented")

// CoeffToken is the first syntax element of a CAVLC residual block.
type CoeffToken struct {
	TotalCoeff   int
	TrailingOnes int
}

type vlcEntry struct {
	bits  string
	token CoeffToken
}

var chromaDC2x2CoeffTokens = []vlcEntry{
	{"01", CoeffToken{0, 0}},
	{"000111", CoeffToken{1, 0}}, {"1", CoeffToken{1, 1}},
	{"000100", CoeffToken{2, 0}}, {"000110", CoeffToken{2, 1}}, {"001", CoeffToken{2, 2}},
	{"000011", CoeffToken{3, 0}}, {"0000011", CoeffToken{3, 1}}, {"0000010", CoeffToken{3, 2}}, {"000101", CoeffToken{3, 3}},
	{"000010", CoeffToken{4, 0}}, {"00000011", CoeffToken{4, 1}}, {"00000010", CoeffToken{4, 2}}, {"0000000", CoeffToken{4, 3}},
}

var coeffTokenCodes = [3][4][16]uint8{
	{
		{5, 7, 7, 7, 7, 15, 11, 8, 15, 11, 15, 11, 15, 11, 7, 4},
		{1, 4, 6, 6, 6, 6, 14, 10, 14, 10, 14, 10, 1, 14, 10, 6},
		{0, 1, 5, 5, 5, 5, 5, 13, 9, 13, 9, 13, 9, 13, 9, 5},
		{0, 0, 3, 3, 4, 4, 4, 4, 4, 12, 12, 8, 12, 8, 12, 8},
	},
	{
		{11, 7, 7, 7, 4, 7, 15, 11, 15, 11, 8, 15, 11, 7, 9, 7},
		{2, 7, 10, 6, 6, 6, 6, 14, 10, 14, 10, 14, 10, 11, 8, 6},
		{0, 3, 9, 5, 5, 5, 5, 13, 9, 13, 9, 13, 9, 6, 10, 5},
		{0, 0, 5, 4, 6, 8, 4, 4, 4, 12, 8, 12, 12, 8, 1, 4},
	},
	{
		{15, 11, 8, 15, 11, 9, 8, 15, 11, 15, 11, 8, 13, 9, 5, 1},
		{14, 15, 12, 10, 8, 14, 10, 14, 14, 10, 14, 10, 7, 12, 8, 4},
		{0, 13, 14, 11, 9, 13, 9, 13, 10, 13, 9, 13, 9, 11, 7, 3},
		{0, 0, 12, 11, 10, 9, 8, 13, 12, 12, 12, 8, 12, 10, 6, 2},
	},
}

var coeffTokenSizes = [3][4][16]uint8{
	{
		{6, 8, 9, 10, 11, 13, 13, 13, 14, 14, 15, 15, 16, 16, 16, 16},
		{2, 6, 8, 9, 10, 11, 13, 13, 14, 14, 15, 15, 15, 16, 16, 16},
		{0, 3, 7, 8, 9, 10, 11, 13, 13, 14, 14, 15, 15, 16, 16, 16},
		{0, 0, 5, 6, 7, 8, 9, 10, 11, 13, 14, 14, 15, 15, 16, 16},
	},
	{
		{6, 6, 7, 8, 8, 9, 11, 11, 12, 12, 12, 13, 13, 13, 14, 14},
		{2, 5, 6, 6, 7, 8, 9, 11, 11, 12, 12, 13, 13, 14, 14, 14},
		{0, 3, 6, 6, 7, 8, 9, 11, 11, 12, 12, 13, 13, 13, 14, 14},
		{0, 0, 4, 4, 5, 6, 6, 7, 9, 11, 11, 12, 13, 13, 13, 14},
	},
	{
		{6, 6, 6, 7, 7, 7, 7, 8, 8, 9, 9, 9, 10, 10, 10, 10},
		{4, 5, 5, 5, 5, 6, 6, 7, 8, 8, 9, 9, 9, 10, 10, 10},
		{0, 4, 5, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 10},
		{0, 0, 4, 4, 4, 4, 4, 5, 6, 7, 8, 8, 9, 10, 10, 10},
	},
}

var totalZeroSizes = [...]uint8{
	1, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 9,
	3, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 6, 6, 6, 6,
	4, 3, 3, 3, 4, 4, 3, 3, 4, 5, 5, 6, 5, 6,
	5, 3, 4, 4, 3, 3, 3, 4, 3, 4, 5, 5, 5,
	4, 4, 4, 3, 3, 3, 3, 3, 4, 5, 4, 5,
	6, 5, 3, 3, 3, 3, 3, 3, 4, 3, 6,
	6, 5, 3, 3, 3, 2, 3, 4, 3, 6,
	6, 4, 5, 3, 2, 2, 3, 3, 6,
	6, 6, 4, 2, 2, 3, 2, 5,
	5, 5, 3, 2, 2, 2, 4,
	4, 4, 3, 3, 1, 3,
	4, 4, 2, 1, 3,
	3, 3, 1, 2,
	2, 2, 1,
	1, 1,
}

var totalZeroCodes = [...]uint8{
	1, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 1,
	7, 6, 5, 4, 3, 5, 4, 3, 2, 3, 2, 3, 2, 1, 0,
	5, 7, 6, 5, 4, 3, 4, 3, 2, 3, 2, 1, 1, 0,
	3, 7, 5, 4, 6, 5, 4, 3, 3, 2, 2, 1, 0,
	5, 4, 3, 7, 6, 5, 4, 3, 2, 1, 1, 0,
	1, 1, 7, 6, 5, 4, 3, 2, 1, 1, 0,
	1, 1, 5, 4, 3, 3, 2, 1, 1, 0,
	1, 1, 1, 3, 3, 2, 2, 1, 0,
	1, 0, 1, 3, 2, 1, 1, 1,
	1, 0, 1, 3, 2, 1, 1,
	0, 1, 1, 2, 1, 3,
	0, 1, 1, 1, 1,
	0, 1, 1, 1,
	0, 1, 1,
	0, 1,
}

var totalZeroOffsets = [...]int{0, 16, 31, 45, 58, 70, 81, 91, 100, 108, 115, 121, 126, 130, 133}
var chromaTotalZeroSizes = [...]uint8{1, 2, 3, 3, 1, 2, 2, 1, 1}
var chromaTotalZeroCodes = [...]uint8{1, 1, 1, 0, 1, 1, 0, 1, 0}

// DecodeCoeffToken decodes coeff_token for luma/chroma-AC contexts and 2x2
// chroma DC. The 2x4 chroma-DC context uses nC=-2 and is not implemented yet.
func DecodeCoeffToken(r *BitReader, nC int) (CoeffToken, error) {
	if r == nil {
		return CoeffToken{}, malformed("nil CAVLC bit reader")
	}
	if nC == -1 {
		return decodeVLCToken(r, chromaDC2x2CoeffTokens)
	}
	if nC < 0 {
		return CoeffToken{}, fmt.Errorf("%w: nC=%d", ErrCAVLCContextUnsupported, nC)
	}
	if nC < 8 {
		table := 0
		if nC >= 4 {
			table = 2
		} else if nC >= 2 {
			table = 1
		}
		entries := make([]vlcEntry, 0, 62)
		zeroCodes := [...]string{"1", "11", "1111"}
		entries = append(entries, vlcEntry{bits: zeroCodes[table], token: CoeffToken{}})
		for trailing := 0; trailing <= 3; trailing++ {
			for total := 1; total <= 16; total++ {
				size := coeffTokenSizes[table][trailing][total-1]
				if size == 0 || trailing > total {
					continue
				}
				entries = append(entries, vlcEntry{
					bits:  fmt.Sprintf("%0*b", int(size), coeffTokenCodes[table][trailing][total-1]),
					token: CoeffToken{TotalCoeff: total, TrailingOnes: trailing},
				})
			}
		}
		return decodeVLCToken(r, entries)
	}
	code, err := r.ReadBits(6)
	if err != nil {
		return CoeffToken{}, malformed("fixed coeff_token is truncated")
	}
	if code == 3 {
		return CoeffToken{}, nil
	}
	token := CoeffToken{TotalCoeff: int(code>>2) + 1, TrailingOnes: int(code & 3)}
	if token.TotalCoeff > 16 || token.TrailingOnes > 3 || token.TrailingOnes > token.TotalCoeff {
		return CoeffToken{}, malformed("invalid fixed coeff_token codeword")
	}
	return token, nil
}

// DecodeTotalZeros decodes total_zeros for 4x4/AC blocks (maxCoeff=15 or 16)
// and 2x2 chroma-DC blocks (maxCoeff=4).
func DecodeTotalZeros(r *BitReader, totalCoeff, maxCoeff int) (int, error) {
	if r == nil || totalCoeff <= 0 || totalCoeff > maxCoeff {
		return 0, malformed("invalid total_zeros parameters")
	}
	if totalCoeff == maxCoeff {
		return 0, nil
	}
	var sizes, codes []uint8
	if maxCoeff == 16 || maxCoeff == 15 {
		start := totalZeroOffsets[totalCoeff-1]
		count := maxCoeff - totalCoeff + 1
		sizes, codes = totalZeroSizes[start:start+count], totalZeroCodes[start:start+count]
	} else if maxCoeff == 4 {
		start := 0
		for previous := 1; previous < totalCoeff; previous++ {
			start += 4 - previous + 1
		}
		count := maxCoeff - totalCoeff + 1
		sizes, codes = chromaTotalZeroSizes[start:start+count], chromaTotalZeroCodes[start:start+count]
	} else {
		return 0, fmt.Errorf("%w: total_zeros maxCoeff=%d", ErrCAVLCContextUnsupported, maxCoeff)
	}
	entries := make([]vlcEntry, len(sizes))
	for zeros := range sizes {
		entries[zeros] = vlcEntry{
			bits:  fmt.Sprintf("%0*b", int(sizes[zeros]), codes[zeros]),
			token: CoeffToken{TotalCoeff: zeros},
		}
	}
	token, err := decodeVLCToken(r, entries)
	return token.TotalCoeff, err
}

// DecodeResidualBlockCAVLC decodes one complete residual block and returns
// coefficients in forward scan order.
func DecodeResidualBlockCAVLC(r *BitReader, nC, maxCoeff int) ([]int64, error) {
	if maxCoeff != 4 && maxCoeff != 15 && maxCoeff != 16 {
		return nil, fmt.Errorf("%w: residual maxCoeff=%d", ErrCAVLCContextUnsupported, maxCoeff)
	}
	token, err := DecodeCoeffToken(r, nC)
	if err != nil {
		return nil, err
	}
	if token.TotalCoeff > maxCoeff {
		return nil, malformed("coeff_token exceeds residual block size")
	}
	result := make([]int64, maxCoeff)
	if token.TotalCoeff == 0 {
		return result, nil
	}
	levels, err := DecodeLevels(r, token)
	if err != nil {
		return nil, err
	}
	totalZeros, err := DecodeTotalZeros(r, token.TotalCoeff, maxCoeff)
	if err != nil {
		return nil, err
	}
	runs := make([]int, token.TotalCoeff)
	zerosLeft := totalZeros
	for i := 0; i < token.TotalCoeff-1; i++ {
		if zerosLeft > 0 {
			runs[i], err = DecodeRunBefore(r, zerosLeft)
			if err != nil {
				return nil, err
			}
		}
		zerosLeft -= runs[i]
	}
	runs[token.TotalCoeff-1] = zerosLeft
	coefficient := -1
	for i := token.TotalCoeff - 1; i >= 0; i-- {
		coefficient += runs[i] + 1
		if coefficient < 0 || coefficient >= len(result) {
			return nil, malformed("CAVLC coefficient position exceeds block")
		}
		result[coefficient] = levels[i]
	}
	return result, nil
}

// DecodeLevels decodes trailing-one signs and the remaining coefficient levels.
// The returned order is the CAVLC level[] order: reverse coefficient scan order.
func DecodeLevels(r *BitReader, token CoeffToken) ([]int64, error) {
	if r == nil || token.TotalCoeff < 0 || token.TotalCoeff > 16 || token.TrailingOnes < 0 ||
		token.TrailingOnes > 3 || token.TrailingOnes > token.TotalCoeff {
		return nil, malformed("invalid CAVLC level parameters")
	}
	levels := make([]int64, token.TotalCoeff)
	for i := 0; i < token.TrailingOnes; i++ {
		sign, err := r.ReadBit()
		if err != nil {
			return nil, malformed("trailing_ones_sign_flag is truncated")
		}
		levels[i] = 1
		if sign != 0 {
			levels[i] = -1
		}
	}
	suffixLength := uint(0)
	if token.TotalCoeff > 10 && token.TrailingOnes < 3 {
		suffixLength = 1
	}
	for i := token.TrailingOnes; i < token.TotalCoeff; i++ {
		prefix, err := readLevelPrefix(r)
		if err != nil {
			return nil, err
		}
		suffixSize := suffixLength
		if prefix == 14 && suffixLength == 0 {
			suffixSize = 4
		} else if prefix >= 15 {
			suffixSize = prefix - 3
		}
		if suffixSize > 63 {
			return nil, malformed("CAVLC level suffix is too large")
		}
		suffix, err := r.ReadBits(suffixSize)
		if err != nil {
			return nil, malformed("level_suffix is truncated")
		}
		basePrefix := prefix
		if basePrefix > 15 {
			basePrefix = 15
		}
		levelCode := (uint64(basePrefix) << suffixLength) + suffix
		if prefix >= 15 && suffixLength == 0 {
			levelCode += 15
		}
		if prefix >= 16 {
			if prefix-3 >= 64 {
				return nil, malformed("CAVLC level prefix overflows")
			}
			levelCode += (uint64(1) << (prefix - 3)) - 4096
		}
		if i == token.TrailingOnes && token.TrailingOnes < 3 {
			levelCode += 2
		}
		if levelCode&1 == 0 {
			levels[i] = int64((levelCode + 2) >> 1)
		} else {
			levels[i] = -int64((levelCode + 1) >> 1)
		}
		if suffixLength == 0 {
			suffixLength = 1
		}
		absolute := levels[i]
		if absolute < 0 {
			absolute = -absolute
		}
		if suffixLength < 6 && absolute > int64(3<<(suffixLength-1)) {
			suffixLength++
		}
	}
	return levels, nil
}

// DecodeRunBefore decodes one run_before for the current zerosLeft value.
func DecodeRunBefore(r *BitReader, zerosLeft int) (int, error) {
	if r == nil || zerosLeft <= 0 {
		return 0, malformed("invalid zerosLeft for run_before")
	}
	if zerosLeft == 1 {
		bit, err := r.ReadBit()
		if err != nil {
			return 0, malformed("run_before is truncated")
		}
		return 1 - int(bit), nil
	}
	tables := map[int][]string{
		2: {"1", "01", "00"},
		3: {"11", "10", "01", "00"},
		4: {"11", "10", "01", "001", "000"},
		5: {"11", "10", "011", "010", "001", "000"},
		6: {"11", "000", "001", "011", "010", "101", "100"},
	}
	if codes, ok := tables[zerosLeft]; ok {
		return decodeVLCIndex(r, codes)
	}
	// For zerosLeft > 6, runs 0..6 use three-bit descending codes. Larger
	// runs are represented by a unary extension: 0001 is 7, 00001 is 8, etc.
	value, err := r.ReadBits(3)
	if err != nil {
		return 0, malformed("run_before is truncated")
	}
	if value != 0 {
		return 7 - int(value), nil
	}
	run := 7
	for run <= zerosLeft {
		bit, err := r.ReadBit()
		if err != nil {
			return 0, malformed("extended run_before is truncated")
		}
		if bit != 0 {
			return run, nil
		}
		run++
	}
	return 0, malformed("run_before exceeds zerosLeft")
}

func readLevelPrefix(r *BitReader) (uint, error) {
	for prefix := uint(0); prefix < 64; prefix++ {
		bit, err := r.ReadBit()
		if err != nil {
			return 0, malformed("level_prefix is truncated")
		}
		if bit != 0 {
			return prefix, nil
		}
	}
	return 0, malformed("level_prefix is too large")
}

func decodeVLCToken(r *BitReader, entries []vlcEntry) (CoeffToken, error) {
	prefix := ""
	for length := 1; length <= 32; length++ {
		bit, err := r.ReadBit()
		if err != nil {
			return CoeffToken{}, malformed("coeff_token is truncated")
		}
		prefix += string('0' + bit)
		possible := false
		for _, entry := range entries {
			if entry.bits == prefix {
				return entry.token, nil
			}
			if len(entry.bits) > len(prefix) && entry.bits[:len(prefix)] == prefix {
				possible = true
			}
		}
		if !possible {
			return CoeffToken{}, fmt.Errorf("%w: invalid coeff_token prefix %q", ErrMalformed, prefix)
		}
	}
	return CoeffToken{}, malformed("coeff_token is too long")
}

func decodeVLCIndex(r *BitReader, codes []string) (int, error) {
	entries := make([]vlcEntry, len(codes))
	for index, code := range codes {
		entries[index] = vlcEntry{bits: code, token: CoeffToken{TotalCoeff: index}}
	}
	token, err := decodeVLCToken(r, entries)
	return token.TotalCoeff, err
}
