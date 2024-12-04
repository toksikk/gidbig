package util

import (
	"math/rand"
	"time"
)

// RandomRange returns a random integer between min and max
func RandomRange(min, max int) int {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return r.Intn(max-min) + min
}
