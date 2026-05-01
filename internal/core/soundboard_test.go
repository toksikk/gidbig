package gidbig

import (
	"testing"
	"time"
)

// TestDAVEHandshakeWarmup_minimum verifies that the DAVE warmup constant is at
// least 400 ms.  Discord's DAVE (E2EE) key exchange requires roughly 100–300 ms
// (two gateway round-trips: key-package → Welcome, then ReadyForTransition →
// ExecuteTransition).  Frames sent before ExecuteTransition is processed are
// discarded by Discord clients in DAVE-enabled channels.  A warmup below 400 ms
// risks the exchange not completing before audio starts, especially on connections
// with higher latency.
func TestDAVEHandshakeWarmup_minimum(t *testing.T) {
	const minWarmup = 400 * time.Millisecond
	if daveHandshakeWarmup < minWarmup {
		t.Errorf("daveHandshakeWarmup = %v; must be >= %v to cover the DAVE key exchange on slow connections", daveHandshakeWarmup, minWarmup)
	}
}
