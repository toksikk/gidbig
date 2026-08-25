package util

import "time"

// Season identifies the seasonal context of a date.
type Season int

const (
	SeasonNone Season = iota
	SeasonNewYear
	SeasonAprilFools
	SeasonEaster
	SeasonHalloween
	SeasonChristmas
)

// CurrentSeason returns the season active on the current local date.
func CurrentSeason() Season {
	return CurrentSeasonAt(time.Now())
}

// CurrentSeasonAt returns the season active at the given time.
// Priority: New Year beats Easter beats Christmas (they cannot overlap,
// but the order documents intent).
func CurrentSeasonAt(now time.Time) Season {
	switch {
	case now.Month() == time.December && now.Day() == 31:
		return SeasonNewYear
	case now.Month() == time.January && now.Day() == 1:
		return SeasonNewYear
	case now.Month() == time.April && now.Day() == 1:
		return SeasonAprilFools
	case now.Month() == time.October && now.Day() == 31:
		return SeasonHalloween
	case inEasterWeek(now):
		return SeasonEaster
	case now.Month() == time.December && (now.Day() == 24 || now.Day() == 25):
		return SeasonChristmas
	default:
		return SeasonNone
	}
}

// inEasterWeek reports whether now falls within Good Friday through
// Easter Monday of the same year.
func inEasterWeek(now time.Time) bool {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	sunday := EasterSunday(now.Year())
	return !today.Before(sunday.AddDate(0, 0, -2)) && today.Before(sunday.AddDate(0, 0, 2))
}

// EasterSunday computes Easter Sunday for the given Gregorian year via the
// Meeus-Jones-Butcher algorithm.
func EasterSunday(year int) time.Time {
	a := year % 19
	b := year / 100
	c := year % 100
	d := b / 4
	e := b % 4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i := c / 4
	k := c % 4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}
