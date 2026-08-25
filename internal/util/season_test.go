package util

import (
	"testing"
	"time"
)

func TestCurrentSeasonAt(t *testing.T) {
	cases := []struct {
		name string
		date time.Time
		want Season
	}{
		{"december 30", time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC), SeasonNone},
		{"silvester", time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), SeasonNewYear},
		{"new year", time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), SeasonNewYear},
		{"january 2", time.Date(2027, 1, 2, 12, 0, 0, 0, time.UTC), SeasonNone},
		{"march 31", time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), SeasonNone},
		{"april 1", time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC), SeasonAprilFools},
		{"april 2", time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC), SeasonNone},
		{"easter week 2026 (2026-04-05 sunday)", time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC), SeasonEaster},
		{"good friday 2026", time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC), SeasonEaster},
		{"easter monday 2026", time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC), SeasonEaster},
		{"day before good friday 2026", time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC), SeasonNone},
		{"day after easter monday 2026", time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC), SeasonNone},
		{"october 30", time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), SeasonNone},
		{"halloween", time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), SeasonHalloween},
		{"november 1", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), SeasonNone},
		{"december 23", time.Date(2026, 12, 23, 12, 0, 0, 0, time.UTC), SeasonNone},
		{"christmas eve", time.Date(2026, 12, 24, 18, 30, 0, 0, time.UTC), SeasonChristmas},
		{"christmas day", time.Date(2026, 12, 25, 18, 30, 0, 0, time.UTC), SeasonChristmas},
		{"december 26", time.Date(2026, 12, 26, 12, 0, 0, 0, time.UTC), SeasonNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CurrentSeasonAt(tc.date); got != tc.want {
				t.Errorf("CurrentSeasonAt(%s) = %v, want %v", tc.date, got, tc.want)
			}
		})
	}
}

func TestCurrentSeasonAt_Timezone(t *testing.T) {
	loc := time.FixedZone("UTC+2", 2*60*60)
	// Nov 1 01:00 UTC+2 is Oct 31 23:00 UTC; local date should win.
	local := time.Date(2026, 11, 1, 1, 0, 0, 0, loc)
	if got := CurrentSeasonAt(local); got != SeasonNone {
		t.Errorf("CurrentSeasonAt(local Nov 1) = %v, want SeasonNone (local date)", got)
	}
}

func TestEasterSunday(t *testing.T) {
	cases := []struct {
		year int
		want string // YYYY-MM-DD
	}{
		{2024, "2024-03-31"},
		{2025, "2025-04-20"},
		{2026, "2026-04-05"},
		{2038, "2038-04-25"},
		{2049, "2049-04-18"},
		{2077, "2077-04-11"},
		{2100, "2100-03-28"},
	}
	for _, tc := range cases {
		got := EasterSunday(tc.year).Format("2006-01-02")
		if got != tc.want {
			t.Errorf("EasterSunday(%d) = %s, want %s", tc.year, got, tc.want)
		}
	}
}
