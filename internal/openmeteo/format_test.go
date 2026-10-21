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

const testHeader = "## 📍 [Winterberg, Deutschland (Nordrhein-Westfalen)](<https://www.google.com/maps/place/51.19,8.53>)\n"

var discordPlace = place{Name: "Winterberg", Admin1: "Nordrhein-Westfalen", Country: "Deutschland", CountryCode: "DE", Latitude: 51.19, Longitude: 8.53}

func TestDiscordCurrentGolden(t *testing.T) {
	want := testHeader + "```\n" +
		"🌡️ 6°C (feels like 3°C)\n" +
		"💧 90% humidity\n" +
		"🌬️ ↙️ 14km/h\n" +
		"🌦️ Light rain\n" +
		"📅 Today: 7°C / 3°C, 🌧️ 3.3mm (63%), ☀️ 3.2h\n" +
		"⚠️ 🌧️ 63%\n" +
		"```\n-# Data: Open-Meteo.com (CC BY 4.0)"
	if got := buildDiscordCurrent(discordPlace, fixtureForecast(1)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDiscordForecastBlocksGolden(t *testing.T) {
	want := testHeader +
		"### 📅 Thu 2026-10-22\n```\n" +
		"🌡️ 7°C / 3°C\n" +
		"🌬️ ↙️ 18km/h (gusts 41km/h)\n" +
		"🌦️ Light rain\n" +
		"🌧️ 3.3mm (63%)\n" +
		"☀️ 3.2h sun\n" +
		"⚠️ 🌧️ 63%\n" +
		"```\n" +
		"### 📅 Fri 2026-10-23\n```\n" +
		"🌡️ 9°C / 2°C\n" +
		"🌬️ ⬅️ 12km/h (gusts 30km/h)\n" +
		"⛅ Partly cloudy\n" +
		"☀️ 6.8h sun\n" +
		"```\n-# Data: Open-Meteo.com (CC BY 4.0)"
	if got := buildDiscordForecast(discordPlace, fixtureForecast(2)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDiscordForecastTableGolden(t *testing.T) {
	out := buildDiscordForecast(discordPlace, fixtureForecast(16))
	lines := strings.Split(out, "\n")
	want := []string{
		strings.TrimSuffix(testHeader, "\n"),
		"```",
		"Day             °C     Rain    %    Sun     Wind",
		"Thu 10-22      7/3    3.3mm  63%   3.2h   18km/h ↙️ 🌦️",
		"Fri 10-23      9/2    0.0mm   5%   6.8h   12km/h ⬅️ ⛅",
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
	// header + ``` + column row + 16 days + ``` + footer
	if len(lines) != 3+16+2 || lines[len(lines)-1] != "-# "+attribution {
		t.Errorf("table output:\n%s", out)
	}
}

func TestDiscordForecastSwitchesToTableAfter7Days(t *testing.T) {
	if out := buildDiscordForecast(discordPlace, fixtureForecast(7)); strings.Count(out, "### 📅") != 7 {
		t.Errorf("7 days should be blocks:\n%s", out)
	}
	if out := buildDiscordForecast(discordPlace, fixtureForecast(8)); strings.Contains(out, "### 📅") {
		t.Errorf("8 days should be a table:\n%s", out)
	}
}

func TestDiscordForecastWithinLimit(t *testing.T) {
	worst := fixtureForecast(16)
	for i := range worst.Daily.Time {
		worst.Daily.SnowSum[i] = 12.5
		worst.Daily.GustsMax[i] = 120
		worst.Daily.WeatherCode[i] = 99
		worst.Daily.PrecipSum[i] = 123.4
		worst.Daily.PrecipProbMax[i] = 100
		worst.Daily.TempMax[i], worst.Daily.TempMin[i] = -12, -25
	}
	long := discordPlace
	long.Name, long.Admin1, long.Country = strings.Repeat("x", 100), strings.Repeat("y", 60), strings.Repeat("z", 40)
	for _, days := range []int{7, 16} {
		fc := worst
		fc.Daily.Time = worst.Daily.Time[:days]
		if n := utf8.RuneCountInString(buildDiscordForecast(long, fc)); n > discordMaxLen {
			t.Errorf("%d days: %d runes > %d", days, n, discordMaxLen)
		}
	}
}

func TestDiscordDayBlockOptionalLines(t *testing.T) {
	fc := fixtureForecast(2)
	dry := discordDayBlock(fc, 1)
	if strings.Contains(dry, "🌧️") || strings.Contains(dry, "❄️") || strings.Contains(dry, "⚠️") {
		t.Errorf("dry day has rain/snow/warn lines:\n%s", dry)
	}
	fc.Daily.SnowSum[1], fc.Daily.GustsMax[1], fc.Daily.WeatherCode[1] = 2.5, 55, 95
	got := discordDayBlock(fc, 1)
	for _, want := range []string{"❄️ 2.5cm\n", "⚠️ 💨 55km/h ⛈️ ❄️\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestWindArrow(t *testing.T) {
	for deg, want := range map[float64]string{0: "⬆️", 22: "⬆️", 23: "↗️", 90: "➡️", 225: "↙️", 270: "⬅️", 300: "↖️", 359: "⬆️", -90: "⬅️"} {
		if got := windArrow(deg); got != want {
			t.Errorf("windArrow(%v) = %q, want %q", deg, got, want)
		}
	}
}

func TestDiscordHeaderCoordinatePlace(t *testing.T) {
	got := discordHeader(place{Name: "Sauerland", Latitude: 51.2, Longitude: 8.4})
	if got != "## 📍 [Sauerland](<https://www.google.com/maps/place/51.20,8.40>)\n" {
		t.Errorf("header = %q", got)
	}
}
