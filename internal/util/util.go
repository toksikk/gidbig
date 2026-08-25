package util

import "math/rand/v2"

// Cl is a byte slice containing the bytes for something special
var Cl []byte = []byte{0xF0, 0x9F, 0xA4, 0xA1}

// Ae is a 2D byte slice containing the bytes for something else
var Ae = [][]byte{
	{0xF0, 0x9F, 0x8D, 0xBA},
	{0xF0, 0x9F, 0x8D, 0xB7},
	{0xF0, 0x9F, 0x8D, 0xB8},
	{0xF0, 0x9F, 0x8D, 0xB9},
	{0xF0, 0x9F, 0x8D, 0xB6},
	{0xF0, 0x9F, 0xA5, 0x83},
}

// RandomRange returns a random integer in [min, max). Returns min when max <= min.
func RandomRange(min, max int) int {
	if max <= min {
		return min
	}
	return rand.IntN(max-min) + min
}
