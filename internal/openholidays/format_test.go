package openholidays

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPickName(t *testing.T) {
	cases := []struct {
		in   []localizedText
		want string
	}{
		{[]localizedText{{"EN", "Christmas"}, {"DE", "Weihnachten"}}, "Weihnachten"},
		{[]localizedText{{"FR", "Noël"}, {"EN", "Christmas"}}, "Christmas"},
		{[]localizedText{{"FR", "Noël"}}, "Noël"},
		{nil, "?"},
	}
	for _, c := range cases {
		if got := pickName(c.in); got != c.want {
			t.Errorf("pickName(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildDiscordPublic(t *testing.T) {
	q := query{country: "DE", region: "DE-NW", kind: typePublic, from: "2026-10-25", to: "2027-10-24"}
	hs := []holiday{
		{StartDate: "2026-11-01", EndDate: "2026-11-01", Name: []localizedText{{"DE", "Allerheiligen"}}, Subdivisions: []subdivisionRef{{"DE-BY", "BY"}, {"DE-NW", "NW"}}},
		{StartDate: "2026-12-25", EndDate: "2026-12-25", Name: []localizedText{{"DE", "1. Weihnachtstag"}}, Nationwide: true},
	}
	out := buildDiscord(q, hs)
	for _, want := range []string{
		"## 🎉 Public holidays DE-NW · 2026-10-25 → 2027-10-24\n",
		"Sun 2026-11-01  Allerheiligen (BY, NW)\n",
		"Fri 2026-12-25  1. Weihnachtstag\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, attribution) {
		t.Errorf("no footer:\n%s", out)
	}
}

func TestBuildDiscordSchool(t *testing.T) {
	q := query{country: "DE", kind: typeSchool, from: "2026-10-25", to: "2027-10-24"}
	hs := []holiday{{StartDate: "2026-12-23", EndDate: "2027-01-06", Name: []localizedText{{"DE", "Weihnachtsferien"}}, Subdivisions: []subdivisionRef{{"DE-NW", "NW"}}}}
	out := buildDiscord(q, hs)
	if !strings.Contains(out, "🏫 School holidays DE ·") || !strings.Contains(out, "Wed 2026-12-23 → Wed 2027-01-06 (15 days)  Weihnachtsferien (NW)") {
		t.Errorf("out:\n%s", out)
	}
}

func TestBuildDiscordEmpty(t *testing.T) {
	out := buildDiscord(query{country: "DE", kind: typePublic, from: "a", to: "b"}, nil)
	if !strings.Contains(out, noHolidaysText) || !strings.HasSuffix(out, attribution) {
		t.Errorf("out:\n%s", out)
	}
}

func TestBuildDiscordTruncates(t *testing.T) {
	var hs []holiday
	for i := range 200 {
		hs = append(hs, holiday{StartDate: "2026-12-25", EndDate: "2026-12-25", Name: []localizedText{{"DE", fmt.Sprintf("Feiertag Nummer %03d mit langem Namen", i)}}, Nationwide: true})
	}
	out := buildDiscord(query{country: "DE", kind: typePublic, from: "a", to: "b"}, hs)
	if n := utf8.RuneCountInString(out); n > maxMessageLen {
		t.Fatalf("len = %d", n)
	}
	if !strings.Contains(out, "…and ") || !strings.Contains(out, " more\n```") || !strings.HasSuffix(out, attribution) {
		t.Errorf("out tail:\n%s", out[len(out)-200:])
	}
	shown := strings.Count(out, "Feiertag Nummer")
	if !strings.Contains(out, fmt.Sprintf("…and %d more", 200-shown)) {
		t.Errorf("shown=%d, more line wrong", shown)
	}
}

func TestFilterRegion(t *testing.T) {
	hs := []holiday{
		{ID: "nat", Nationwide: true},
		{ID: "by", Subdivisions: []subdivisionRef{{Code: "DE-BY"}}},
		{ID: "by-au", Subdivisions: []subdivisionRef{{Code: "DE-BY-AU"}}},
		{ID: "nw", Subdivisions: []subdivisionRef{{Code: "DE-NW"}}},
	}
	var ids []string
	for _, h := range filterRegion(hs, "DE-BY") {
		ids = append(ids, h.ID)
	}
	if strings.Join(ids, ",") != "nat,by,by-au" {
		t.Fatalf("ids = %v", ids)
	}
	if len(filterRegion(hs, "")) != 4 {
		t.Fatal("empty region filtered")
	}
}

func TestBuildToolText(t *testing.T) {
	q := query{country: "DE", region: "DE-BY", kind: typePublic, from: "2026-10-25", to: "2026-11-24"}
	hs := []holiday{
		{StartDate: "2026-11-01", EndDate: "2026-11-01", Type: "Public", Name: []localizedText{{"DE", "Allerheiligen"}}, Subdivisions: []subdivisionRef{{"DE-BY", "BY"}}},
		{StartDate: "2026-12-23", EndDate: "2027-01-06", Type: "School", Name: []localizedText{{"DE", "Ferien"}}, Nationwide: true},
	}
	out := buildToolText(q, hs)
	for _, want := range []string{
		"public holidays in DE-BY from 2026-10-25 to 2026-11-24",
		"2026-11-01 (Sun): Allerheiligen; regions: BY; public",
		"2026-12-23 (Wed) to 2027-01-06 (Wed): Ferien; nationwide; school",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
