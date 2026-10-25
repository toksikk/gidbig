package tv

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNowNext_HalfOpenBoundary(t *testing.T) {
	ix := fixtureIndex(t)
	at := time.Date(2026, 10, 24, 20, 15, 0, 0, berlin)
	cur, next := ix.nowNext(0, at)
	if cur == nil || cur.Title != "Der Alte" || next == nil || next.Title != "heute journal" {
		t.Fatalf("at start: cur=%v next=%v", cur, next)
	}
	cur, next = ix.nowNext(0, at.Add(-time.Second))
	// 19:15-20:15 was a dropped "No Data" gap.
	if cur != nil || next == nil || next.Title != "Der Alte" {
		t.Fatalf("in gap: cur=%v next=%v", cur, next)
	}
	cur, next = ix.nowNext(0, time.Date(2026, 10, 25, 3, 0, 0, 0, berlin))
	if cur != nil || next != nil {
		t.Fatalf("after end: cur=%v next=%v", cur, next)
	}
}

func TestDay_AcrossMidnightBerlin(t *testing.T) {
	ix := fixtureIndex(t)
	got := strings.Join(titles(ix.day(0, fixtureNow, berlin)), "|")
	// heute journal runs 21:45-01:00 and overlaps both days.
	if got != "heute|Der Alte|heute journal" {
		t.Errorf("24th = %q", got)
	}
	got = strings.Join(titles(ix.day(0, fixtureNow.AddDate(0, 0, 1), berlin)), "|")
	if got != "heute journal|Nachtfilm" {
		t.Errorf("25th = %q", got)
	}
	if got := titles(ix.day(0, fixtureNow.AddDate(0, 0, -1), berlin)); len(got) != 1 || got[0] != "Morgenmagazin" {
		t.Errorf("23rd = %q", got)
	}
	if ps := ix.day(0, fixtureNow.AddDate(0, 0, 3), berlin); len(ps) != 0 {
		t.Errorf("27th = %v", titles(ps))
	}
}

func TestSearch(t *testing.T) {
	ix := fixtureIndex(t)
	for _, q := range []string{"tatort", "TATORT", " Tatort "} {
		hits := ix.search(q, fixtureNow, 10)
		if len(hits) != 2 || hits[0].p.Sub != "Der letzte Schrei" || hits[1].p.Sub != "Wiederholung" {
			t.Errorf("search(%q) = %+v", q, hits)
		}
	}
	if hits := ix.search("tatort", fixtureNow, 1); len(hits) != 1 {
		t.Errorf("limit: %d hits", len(hits))
	}
	for _, q := range []string{"muenchen", "münchen", "MÜNCHEN"} {
		if hits := ix.search(q, fixtureNow, 10); len(hits) != 1 || hits[0].p.Title != "Der Alte" {
			t.Errorf("search(%q) = %+v", q, hits)
		}
	}
	// Ended shows are not returned.
	if hits := ix.search("heute", fixtureNow, 10); len(hits) != 1 || hits[0].p.Title != "heute journal" {
		t.Errorf("search(heute) = %+v", hits)
	}
	if hits := ix.search("  ", fixtureNow, 10); hits != nil {
		t.Errorf("empty query = %+v", hits)
	}
}

func TestResolveChannel(t *testing.T) {
	ix := fixtureIndex(t)
	for name, want := range map[string]int{"ARD": 1, "das erste": 1, "DasErste.de": 1, "zdf": 0, "Z.D.F.": 0} {
		if got, ok := ix.resolveChannel(name); !ok || got != want {
			t.Errorf("resolve(%q) = %d %v, want %d", name, got, ok, want)
		}
	}
	if _, ok := ix.resolveChannel("Other"); ok {
		t.Error("non-allowlisted channel resolved")
	}
	ix2 := newIndex([]channelInfo{{ID: "zdf_neo.de", Name: "zdf_neo"}}, [][]programme{nil}, fixtureNow, berlin)
	if _, ok := ix2.resolveChannel("zdf neo"); !ok {
		t.Error("zdf neo not resolved")
	}
	if got := ix.suggest("erst", 5); len(got) != 1 || got[0] != 1 {
		t.Errorf("suggest = %v", got)
	}
	if got := ix.suggest("", 1); len(got) != 1 {
		t.Errorf("suggest limit = %v", got)
	}
}

func TestChannelLabel(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"DasErste.de", "DE - Das Erste"}: "Das Erste",
		{"zdf_neo.de", "DE - zdf_neo"}:    "ZDFneo",
		{"Foo.de", ""}:                    "Foo",
		{"ORF1.at", "AT - ORF 1"}:         "ORF 1",
		{"weird", ""}:                     "weird",
	} {
		if got := channelLabel(in[0], in[1]); got != want {
			t.Errorf("channelLabel(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// listingsIndex builds a synthetic index: two channels, programmes every
// hour from today 00:00 for the given days, alternating genres.
func listingsIndex(days int) *index {
	chs := []channelInfo{{ID: "A.de", Name: "Alpha"}, {ID: "B.de", Name: "Beta"}}
	progs := make([][]programme, len(chs))
	start := time.Date(2026, 10, 24, 0, 0, 0, 0, berlin)
	for ci := range chs {
		for h := 0; h < days*24; h++ {
			s := start.Add(time.Duration(h) * time.Hour)
			cats := []string{"Show"}
			if h%2 == 0 {
				cats = []string{"Spielshow", "Krimi"}
			}
			progs[ci] = append(progs[ci], programme{Start: s.Unix(), Stop: s.Add(time.Hour).Unix(), Title: fmt.Sprintf("P%d-%d", ci, h), Categories: cats})
		}
	}
	return newIndex(chs, progs, fixtureNow, berlin)
}

func allChs(ix *index) []int { chs, _ := resolveOrAll(ix, ""); return chs }

func TestListings_CategoryAnyMatch(t *testing.T) {
	ix := listingsIndex(1)
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, berlin)
	hits := ix.listings(listingFilter{category: foldText("KRIMI"), slotTo: 24 * 60, chs: allChs(ix), days: 1}, now)
	if len(hits) != 24 {
		t.Fatalf("secondary category hits = %d, want 24", len(hits))
	}
	if hits := ix.listings(listingFilter{category: "spielfilm", slotTo: 24 * 60, chs: allChs(ix), days: 1}, now); len(hits) != 0 {
		t.Errorf("miss = %d hits", len(hits))
	}
	if !ix.hasCategory("krimi") || ix.hasCategory("spielfilm") {
		t.Error("hasCategory")
	}
}

func TestListings_SlotBoundsAndWrap(t *testing.T) {
	ix := listingsIndex(2)
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, berlin)
	hits := ix.listings(listingFilter{slotFrom: 20 * 60, slotTo: 23 * 60, chs: []int{0}, days: 1}, now)
	if got := strings.Join(hitTitles(hits), "|"); got != "P0-20|P0-21|P0-22" {
		t.Errorf("20-23 = %q", got)
	}
	hits = ix.listings(listingFilter{slotFrom: 23 * 60, slotTo: 1 * 60, chs: []int{0}, days: 2}, now)
	if got := strings.Join(hitTitles(hits), "|"); got != "P0-0|P0-23|P0-24" {
		t.Errorf("wrap = %q", got)
	}
	// Ended programmes are skipped; ordering is start, then channel order.
	hits = ix.listings(listingFilter{slotFrom: 20 * 60, slotTo: 21 * 60, chs: []int{1, 0}, days: 1}, fixtureNow)
	if got := strings.Join(hitTitles(hits), "|"); got != "P1-20|P0-20" {
		t.Errorf("order = %q", got)
	}
}

func hitTitles(hs []hit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.p.Title
	}
	return out
}

func TestRenderListings_Paging(t *testing.T) {
	ix := listingsIndex(7)
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, berlin)
	f := listingFilter{slotTo: 24 * 60, chs: allChs(ix), days: 7}
	total := len(ix.listings(f, now))
	seen := map[string]bool{}
	offset, pages := 0, 0
	for {
		out := renderListings(ix, f, "", now, offset)
		if n := len([]rune(out)); n > discordLimit {
			t.Fatalf("page %d has %d runes", pages, n)
		}
		lines := strings.Split(out, "\n")
		rows := lines[1:]
		last := lines[len(lines)-1]
		next := -1
		if strings.HasPrefix(last, "+") {
			rows = rows[:len(rows)-1]
			var rest int
			if _, err := fmt.Sscanf(last, "+%d more, use offset=%d", &rest, &next); err != nil {
				t.Fatalf("footer %q: %v", last, err)
			}
			if next != offset+len(rows) || rest != total-next {
				t.Fatalf("footer %q at offset %d with %d rows", last, offset, len(rows))
			}
		}
		for _, r := range rows {
			if seen[r] {
				t.Fatalf("duplicate row %q", r)
			}
			seen[r] = true
		}
		pages++
		if next < 0 {
			break
		}
		offset = next
	}
	if len(seen) != total || pages < 2 {
		t.Errorf("rows %d of %d over %d pages", len(seen), total, pages)
	}
}

func TestRenderListings_EmptyMessages(t *testing.T) {
	ix := listingsIndex(1)
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, berlin)
	chs := allChs(ix)
	if out := renderListings(ix, listingFilter{category: "spielfilm", slotTo: 1440, chs: chs, days: 1}, "Spielfilm", now, 0); !strings.Contains(out, "category “Spielfilm”") || !strings.Contains(out, genreHint) {
		t.Errorf("no category: %q", out)
	}
	if out := renderListings(ix, listingFilter{category: "krimi", slotFrom: 21 * 60, slotTo: 22 * 60, chs: chs, days: 1}, "Krimi", now, 0); !strings.Contains(out, "No matching programme starts in that time slot") {
		t.Errorf("empty slot: %q", out)
	}
	if out := renderListings(ix, listingFilter{category: "krimi", slotTo: 1440, chs: chs, days: 1}, "Krimi", now, 99); !strings.Contains(out, "offset 99 is past the end (24 results)") {
		t.Errorf("past end: %q", out)
	}
	if out := renderSearch(ix, "Krimi", now, 5, false); !strings.Contains(out, "search matches titles only; for genres use mode listings") {
		t.Errorf("search miss: %q", out)
	}
}
