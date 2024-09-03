package util

import (
	"math/rand"
	"time"
)

// RandomRange returns a random integer between min and max
func RandomRange(min, max int) int {
	rand.New(rand.NewSource(time.Now().UnixNano()))
	return rand.Intn(max-min) + min
}
