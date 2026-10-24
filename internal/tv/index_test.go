package tv

import (
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
