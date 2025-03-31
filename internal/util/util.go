package util

import (
	"math/rand"
	"time"
)

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

var da byte = byte(0b00100101) ^ byte(0x13) ^ byte(0x37)
var mo byte = byte(0b00100000) ^ byte(0x13) ^ byte(0x37)

// RandomRange returns a random integer between min and max
func RandomRange(min, max int) int {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return r.Intn(max-min) + min
}

// IsSpecial returns true if today is a special day
func IsSpecial() bool {
	today := time.Now()
	return today.Day() == int(da) && today.Month() == time.Month(int(mo))
}
