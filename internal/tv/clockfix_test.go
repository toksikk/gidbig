package tv

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// primeTimeFeed builds three evenings for two channels: Shifted.de with its
// 20:15 film labelled 19:15 and a late show at 21:47, Correct.de on time.
func primeTimeFeed() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><tv>`)
	row := func(ch string, day, h1, m1, h2, m2 int, title string) {
		fmt.Fprintf(&b, `<programme start="202610%d%02d%02d00 +0100" stop="202610%d%02d%02d00 +0100" channel="%s"><title>%s</title></programme>`,
			day, h1, m1, day, h2, m2, ch, title)
	}
	for _, day := range []int{26, 27, 28} {
		row("Shifted.de", day, 19, 15, 21, 47, "Film")
		row("Shifted.de", day, 21, 47, 22, 59, "Late Show")
		row("Correct.de", day, 20, 15, 21, 45, "Film")
		row("Correct.de", day, 21, 45, 22, 15, "News")
	}
	b.WriteString(`</tv>`)
	return b.String()
}

func TestParse_CorrectsShiftedChannelClock(t *testing.T) {
	now := time.Date(2026, 10, 26, 22, 2, 0, 0, berlin)
	ix, err := parse(strings.NewReader(primeTimeFeed()), []string{"Shifted.de", "Correct.de"}, now, berlin)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	span := func(p programme) string { return clock(p.Start, berlin) + "-" + clock(p.Stop, berlin) }
	if got := span(ix.progs[0][1]); got != "22:47-23:59" {
		t.Errorf("shifted late show = %s, want 22:47-23:59", got)
	}
	if got := span(ix.progs[1][0]); got != "20:15-21:45" {
		t.Errorf("correct film = %s, want 20:15-21:45", got)
	}
}

func TestPrimeTimeShift(t *testing.T) {
	at := func(day, h, m int) programme {
		return programme{Start: time.Date(2026, 10, day, h, m, 0, 0, berlin).Unix()}
	}
	cases := []struct {
		name string
		ps   []programme
		want time.Duration
	}{
		{"shifted", []programme{at(26, 19, 15), at(27, 19, 15), at(28, 19, 15)}, time.Hour},
		{"too few votes", []programme{at(26, 19, 15), at(27, 19, 15)}, 0},
		{"stray 20:15", []programme{at(26, 19, 15), at(27, 19, 15), at(28, 19, 15), at(29, 20, 15)}, time.Hour},
		{"mixed", []programme{at(26, 19, 15), at(27, 19, 15), at(28, 19, 15), at(29, 20, 15), at(30, 20, 15)}, 0},
		{"on time", []programme{at(26, 20, 15), at(27, 20, 15), at(28, 20, 15)}, 0},
		{"empty", nil, 0},
	}
	for _, c := range cases {
		if got := primeTimeShift(c.ps, 0, math.MaxInt64); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// A correct stale row from last season outside the window does not vote.
	stale := []programme{at(26, 19, 15), at(27, 19, 15), at(28, 19, 15), {Start: time.Date(2025, 10, 8, 20, 15, 0, 0, berlin).Unix()}}
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, berlin).Unix()
	if got := primeTimeShift(stale, from, math.MaxInt64); got != time.Hour {
		t.Errorf("stale row voted: got %v", got)
	}
}

func TestShiftProgrammes_KeepsMissingStop(t *testing.T) {
	ps := []programme{{Start: 100, Stop: 200}, {Start: 300}}
	shiftProgrammes(ps, time.Minute)
	if ps[0].Start != 160 || ps[0].Stop != 260 || ps[1].Start != 360 || ps[1].Stop != 0 {
		t.Fatalf("shifted = %+v", ps)
	}
}
