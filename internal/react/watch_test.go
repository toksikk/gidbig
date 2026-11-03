package react

import (
	"fmt"
	"sync"
	"testing"
)

func TestWatchStore(t *testing.T) {
	w := newWatchStore(maxWatches)
	k := watchKey{"m", "e", "u"}
	w.add(k)
	w.add(k)
	if !w.has(k) || w.len() != 1 {
		t.Fatalf("has=%v len=%d", w.has(k), w.len())
	}
	if w.has(watchKey{"m", "e", "other"}) {
		t.Fatal("other user matched")
	}
}

func TestWatchStoreEviction(t *testing.T) {
	w := newWatchStore(maxWatches)
	for i := range maxWatches + 1 {
		w.add(watchKey{messageID: fmt.Sprint(i)})
	}
	if w.len() != maxWatches || w.has(watchKey{messageID: "0"}) || !w.has(watchKey{messageID: "1"}) {
		t.Fatalf("eviction wrong: len=%d", w.len())
	}

	// Re-adding refreshes position, so "1" survives the next eviction.
	w.add(watchKey{messageID: "1"})
	w.add(watchKey{messageID: "new"})
	if !w.has(watchKey{messageID: "1"}) || w.has(watchKey{messageID: "2"}) {
		t.Fatal("refresh did not move entry to newest")
	}
}

func TestWatchStoreConcurrent(t *testing.T) {
	w := newWatchStore(50)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				k := watchKey{messageID: fmt.Sprint(g, i)}
				w.add(k)
				w.has(k)
			}
		}()
	}
	wg.Wait()
	if w.len() != 50 {
		t.Fatalf("len=%d", w.len())
	}
}
