// Package mp4 parses ISO Base Media File Format containers without external
// dependencies. Codec bitstreams are intentionally handled by other packages.
package mp4

import "fmt"

// FourCC is a four-byte MP4 box or codec identifier.
type FourCC [4]byte

// Type converts a four-character string to a FourCC.
func Type(value string) FourCC {
	var result FourCC
	copy(result[:], value)
	return result
}

func (f FourCC) String() string { return string(f[:]) }

func (f FourCC) GoString() string { return fmt.Sprintf("mp4.Type(%q)", f.String()) }
