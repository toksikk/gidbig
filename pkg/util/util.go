package util

import (
	"math/rand"
	"time"
)

// RandomRange returns a random integer between min and max
func RandomRange(min, max int) int {
	rand.Seed(time.Now().UTC().UnixNano())
	return rand.Intn(max-min) + min
}
