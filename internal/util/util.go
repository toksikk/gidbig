package util

import (
	"math/rand"
	"time"
)

// Cl is a byte slice containing the bytes for something special
var Cl []byte = []byte{0xF0, 0x9F, 0xA4, 0xA1}
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
