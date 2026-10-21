package openmeteo

import (
	"strings"
	"testing"
	"unicode/utf8"
)

var testPlace = place{Name: "Winterberg", Admin1: "Nordrhein-Westfalen", CountryCode: "DE", Latitude: 51.19, Longitude: 8.53}

func TestBuildToolTextForecast(t *testing.T) {
	out := buildToolText(testPlace, fixtureForecast(7), true)
	lines := strings.Split(out, "\n")
	// location + now + 7 day lines + 3*8 slots
	if want := 2 + 7 + 3*8; len(lines) != want {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), want, out)
	}
	if lines[0] != "Location: Winterberg, Nordrhein-Westfalen, DE (51.19,8.53; tz Europe/Berlin)" {
		t.Errorf("location line = %q", lines[0])
	}
	if lines[1] != "Now: 6°C (feels 3°C), light rain, humidity 90%, cloud 100%, precipitation 0.0mm, wind 14 km/h SW" {
		t.Errorf("now line = %q", lines[1])
	}
	wantDay := "Thu 2026-10-22: light rain, 3–7°C, rain 3.3mm (max 63%), sun 3.2h, wind 18 km/h gusts 41 SW, uv 1, sunrise 07:58 sunset 18:21"
	if lines[2] != wantDay {
		t.Errorf("day line = %q", lines[2])
	}
	// slot 03h: precip summed 3×0.1, prob max of hours 3,4,5
	if lines[4] != "  03h 4°C clear, cloud 6%, rain 0.3mm 5%" {
		t.Errorf("slot line = %q", lines[4])
	}
	// day 4 has no slots: day line directly followed by day 5
	if !strings.HasPrefix(lines[2+3*9], "Sun 2026-10-25:") || !strings.HasPrefix(lines[2+3*9+1], "Mon 2026-10-26:") {
		t.Errorf("day 4/5 lines = %q / %q", lines[2+3*9], lines[2+3*9+1])
	}
}

func TestBuildToolTextCurrentOnlyToday(t *testing.T) {
	out := buildToolText(testPlace, fixtureForecast(7), false)
	if strings.Contains(out, "2026-10-23") || !strings.Contains(out, "2026-10-22") {
		t.Errorf("current output:\n%s", out)
	}
}

func TestWMOUnknown(t *testing.T) {
	if got := wmo(42).desc; got != "unknown (42)" {
		t.Errorf("got %q", got)
	}
}

func TestCompass(t *testing.T) {
	for deg, want := range map[float64]string{350: "N", 0: "N", 45: "NE", 225: "SW", 270: "W", 359.9: "N", -10: "N"} {
		if got := compass(deg); got != want {
			t.Errorf("compass(%v) = %q, want %q", deg, got, want)
		}
	}
}

func TestDiscordForecastTruncates(t *testing.T) {
	out := buildDiscordForecast(testPlace, fixtureForecast(16), 900)
	if n := utf8.RuneCountInString(out); n > 900 {
		t.Fatalf("len %d > 900", n)
	}
	if !strings.Contains(out, truncatedHint) || !strings.HasSuffix(out, attribution) {
		t.Errorf("missing hint or attribution:\n%s", out)
	}
}

func TestDiscordForecast16DaysFitsOrTruncates(t *testing.T) {
	out := buildDiscordForecast(testPlace, fixtureForecast(16), discordMaxLen)
	if n := utf8.RuneCountInString(out); n > discordMaxLen {
		t.Fatalf("len %d > %d", n, discordMaxLen)
	}
	if !strings.Contains(out, "Nordrhein-Westfalen") || !strings.HasSuffix(out, attribution) {
		t.Errorf("output:\n%s", out)
	}
}

func TestDiscordForecastNoTruncation(t *testing.T) {
	out := buildDiscordForecast(testPlace, fixtureForecast(3), discordMaxLen)
	if strings.Contains(out, truncatedHint) || strings.Count(out, "🌡️") != 3 {
		t.Errorf("output:\n%s", out)
	}
}

func TestDiscordCurrent(t *testing.T) {
	out := buildDiscordCurrent(testPlace, fixtureForecast(1))
	for _, want := range []string{"Winterberg", "6°C", "2026-10-22", attribution} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
