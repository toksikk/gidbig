package leetoclock

import (
	"sync"
	"testing"

	"github.com/toksikk/gidbig/internal/leetoclock/util/datastore"
)

func TestSortScoreArrayByScore(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want []int
	}{
		{
			name: "empty slice",
			in:   []int{},
			want: []int{},
		},
		{
			name: "single element",
			in:   []int{42},
			want: []int{42},
		},
		{
			name: "already sorted",
			in:   []int{-3000, -100, 0, 250, 1337},
			want: []int{-3000, -100, 0, 250, 1337},
		},
		{
			name: "reverse sorted",
			in:   []int{1337, 250, 0, -100, -3000},
			want: []int{-3000, -100, 0, 250, 1337},
		},
		{
			name: "with duplicates",
			in:   []int{500, 100, 500, 100, 0},
			want: []int{0, 100, 100, 500, 500},
		},
		{
			name: "negative and positive scores",
			in:   []int{120, -50, 0, -2000, 75},
			want: []int{-2000, -50, 0, 75, 120},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scores := make([]datastore.Score, len(tc.in))
			for i, s := range tc.in {
				scores[i] = datastore.Score{Score: s}
			}

			got := sortScoreArrayByScore(scores)

			if len(got) != len(tc.want) {
				t.Fatalf("length mismatch: got %d, want %d", len(got), len(tc.want))
			}
			for i, want := range tc.want {
				if got[i].Score != want {
					t.Errorf("index %d: got %d, want %d", i, got[i].Score, want)
				}
			}
		})
	}
}

// TestAnnouncePreparationSelfGuard verifies announcePreparation self-guards via
// its internal TryLock. Run with -race to detect data races on shared state.
// session is nil and tt is zero, so isOnTargetTimeRange returns false and no
// Discord calls are made.
func TestAnnouncePreparationSelfGuard(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			announcePreparation()
		}()
	}
	wg.Wait()
}

// TestAnnounceTodaysWinnersSelfGuard verifies announceTodaysWinners self-guards
// via its internal TryLock. Run with -race to detect data races on shared state.
// session is nil and tt is zero, so isOnTargetTimeRange returns false and no
// Discord/store calls are made.
func TestAnnounceTodaysWinnersSelfGuard(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			announceTodaysWinners()
		}()
	}
	wg.Wait()
}

func TestSortScoreArrayByScorePreservesOtherFields(t *testing.T) {
	scores := []datastore.Score{
		{Score: 300, MessageID: "c"},
		{Score: 100, MessageID: "a"},
		{Score: 200, MessageID: "b"},
	}

	got := sortScoreArrayByScore(scores)

	wantOrder := []string{"a", "b", "c"}
	for i, want := range wantOrder {
		if got[i].MessageID != want {
			t.Errorf("index %d: got MessageID %q, want %q", i, got[i].MessageID, want)
		}
	}
}
