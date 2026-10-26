package tv

import "time"

// Since the 2026-10-25 DST switch the free-epg.de feed labels some channels'
// UTC wall-clock times with the German offset, so their rows show one hour
// early (Das Erste's 20:00 Tagesschau appears at 19:00, prime time at 19:15).
// Other channels in the same feed are correct. German channels start prime
// time at 20:15, so a channel whose evening programmes repeatedly start at
// 19:15 and rarely at 20:15 is treated as shifted and moved one hour later.
const (
	// primeTimeMinVotes is how many 19:15 starts a channel needs before its
	// rows are shifted; a few days of feed give one vote per day.
	primeTimeMinVotes = 3
	primeTimeShiftBy  = time.Hour
)

// germanTime is the zone prime time is defined in, independent of the
// configured display zone. Nil when tzdata is missing; detection is then off.
var germanTime, _ = time.LoadLocation("Europe/Berlin")

// primeTimeShift returns the correction for one channel's rows: one hour when
// at least primeTimeMinVotes rows start at 19:15 German time and more than
// twice as many as at 20:15, otherwise zero. Only rows starting in
// [from, to) vote: the feed also carries stale rows from earlier seasons that were labelled correctly.
func primeTimeShift(ps []programme, from, to int64) time.Duration {
	if germanTime == nil {
		return 0
	}
	early, onTime := 0, 0
	for _, p := range ps {
		if p.Start < from || p.Start >= to {
			continue
		}
		t := time.Unix(p.Start, 0).In(germanTime)
		if t.Minute() != 15 {
			continue
		}
		switch t.Hour() {
		case 19:
			early++
		case 20:
			onTime++
		}
	}
	if early >= primeTimeMinVotes && early > 2*onTime {
		return primeTimeShiftBy
	}
	return 0
}

// shiftProgrammes moves every row's start and known stop by d.
func shiftProgrammes(ps []programme, d time.Duration) {
	s := int64(d / time.Second)
	for i := range ps {
		ps[i].Start += s
		if ps[i].Stop != 0 {
			ps[i].Stop += s
		}
	}
}
