package react

import (
	"testing"
	"time"
)

func TestSessionStore(t *testing.T) {
	s := newSessionStore()
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }

	a, err := s.put(&pickerSession{invokerID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.put(&pickerSession{invokerID: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if a == b || len(a) != 16 {
		t.Fatalf("tokens %q %q", a, b)
	}
	if p, ok := s.get(a); !ok || p.invokerID != "u" {
		t.Fatalf("get fresh: %v %v", p, ok)
	}

	now = now.Add(sessionTTL)
	if _, ok := s.get(a); ok {
		t.Fatal("get after TTL succeeded")
	}
	s.sweep(now)
	if s.len() != 0 {
		t.Fatalf("sweep left %d sessions", s.len())
	}
}
