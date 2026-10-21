package openmeteo

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	discordMaxLen = 2000
	detailDays    = 3
	attribution   = "Daten: Open-Meteo.com (CC BY 4.0)"
	truncatedHint = "… (gekürzt)"
)

var compassPoints = []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}

// compass converts degrees to an 8-point compass direction.
func compass(deg float64) string {
	d := math.Mod(deg, 360)
	if d < 0 {
		d += 360
	}
	return compassPoints[int(math.Round(d/45))%8]
}

// sunHours formats sunshine seconds as hours with one decimal.
func sunHours(sec float64) string {
	return fmt.Sprintf("%.1fh", sec/3600)
}

// weekday returns the short English weekday of an ISO date, or "" when it cannot be parsed.
func weekday(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	return t.Weekday().String()[:3]
}

// clock extracts HH:MM from an ISO local time like 2026-10-22T07:58.
func clock(iso string) string {
	if i := strings.IndexByte(iso, 'T'); i >= 0 && len(iso) >= i+6 {
		return iso[i+1 : i+6]
	}
	return iso
}

func placeLabel(p place) string {
	parts := []string{p.Name}
	if p.Admin1 != "" && p.Admin1 != p.Name {
		parts = append(parts, p.Admin1)
	}
	switch {
	case p.CountryCode != "":
		parts = append(parts, p.CountryCode)
	case p.Country != "":
		parts = append(parts, p.Country)
	}
	return strings.Join(parts, ", ")
}

func at[T any](s []T, i int) T {
	var zero T
	if i < 0 || i >= len(s) {
		return zero
	}
	return s[i]
}

// slot is one 3-hour block of the hourly breakdown.
type slot struct {
	hour       int
	temp       float64
	code       int
	cloud      float64
	precip     float64
	precipProb float64
}

// slotsForDay returns the eight 3-hour slots of date. Precipitation is summed
// over the three hours and the probability is their maximum.
func slotsForDay(fc forecastResponse, date string) []slot {
	idx := make(map[string]int, len(fc.Hourly.Time))
	for i, t := range fc.Hourly.Time {
		idx[t] = i
	}
	var out []slot
	for h := 0; h < 24; h += 3 {
		i, ok := idx[fmt.Sprintf("%sT%02d:00", date, h)]
		if !ok {
			continue
		}
		s := slot{
			hour:  h,
			temp:  at(fc.Hourly.Temperature, i),
			code:  at(fc.Hourly.WeatherCode, i),
			cloud: at(fc.Hourly.CloudCover, i),
		}
		for j := i; j < i+3 && j < len(fc.Hourly.Time); j++ {
			s.precip += at(fc.Hourly.Precipitation, j)
			s.precipProb = math.Max(s.precipProb, at(fc.Hourly.PrecipProb, j))
		}
		out = append(out, s)
	}
	return out
}

func dayLine(fc forecastResponse, i int) string {
	d := fc.Daily
	date := at(d.Time, i)
	return fmt.Sprintf("%s %s: %s, %.0f–%.0f°C, rain %.1fmm (max %.0f%%), sun %s, wind %.0f km/h gusts %.0f %s, uv %.0f, sunrise %s sunset %s",
		weekday(date), date, wmo(at(d.WeatherCode, i)).desc,
		at(d.TempMin, i), at(d.TempMax, i),
		at(d.PrecipSum, i), at(d.PrecipProbMax, i),
		sunHours(at(d.Sunshine, i)),
		at(d.WindMax, i), at(d.GustsMax, i), compass(at(d.WindDir, i)),
		at(d.UVMax, i), clock(at(d.Sunrise, i)), clock(at(d.Sunset, i)))
}

func nowLine(fc forecastResponse) string {
	c := fc.Current
	return fmt.Sprintf("Now: %.0f°C (feels %.0f°C), %s, humidity %.0f%%, cloud %.0f%%, precipitation %.1fmm, wind %.0f km/h %s",
		c.Temperature, c.Apparent, wmo(c.WeatherCode).desc, c.Humidity, c.CloudCover, c.Precipitation, c.WindSpeed, compass(c.WindDirection))
}

// buildToolText renders plain text for the LLM tool. Without forecast it
// covers current conditions and today; with forecast every day, with a
// 3-hour breakdown for the first detailDays days.
func buildToolText(p place, fc forecastResponse, forecast bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Location: %s (%.2f,%.2f; tz %s)\n", placeLabel(p), p.Latitude, p.Longitude, fc.Timezone)
	b.WriteString(nowLine(fc))
	b.WriteByte('\n')
	days := len(fc.Daily.Time)
	if !forecast {
		days = min(days, 1)
	}
	for i := 0; i < days; i++ {
		b.WriteString(dayLine(fc, i))
		b.WriteByte('\n')
		if i >= detailDays {
			continue
		}
		for _, s := range slotsForDay(fc, fc.Daily.Time[i]) {
			fmt.Fprintf(&b, "  %02dh %.0f°C %s, cloud %.0f%%, rain %.1fmm %.0f%%\n",
				s.hour, s.temp, wmo(s.code).desc, s.cloud, s.precip, s.precipProb)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func discordHeader(p place) string {
	return fmt.Sprintf("**%s** (%.2f, %.2f)\n", placeLabel(p), p.Latitude, p.Longitude)
}

func discordFooter() string {
	return "-# " + attribution
}

// buildDiscordCurrent renders current conditions plus today for /weather.
func buildDiscordCurrent(p place, fc forecastResponse) string {
	c := fc.Current
	w := wmo(c.WeatherCode)
	var b strings.Builder
	b.WriteString(discordHeader(p))
	fmt.Fprintf(&b, "%s %s, 🌡️ %.0f°C (gefühlt %.0f°C), 💧 %.0f%%, ☁️ %.0f%%, 🌧️ %.1f mm, 💨 %.0f km/h %s\n",
		w.emoji, w.desc, c.Temperature, c.Apparent, c.Humidity, c.CloudCover, c.Precipitation, c.WindSpeed, compass(c.WindDirection))
	if len(fc.Daily.Time) > 0 {
		b.WriteString(discordDay(fc, 0))
	}
	b.WriteString(discordFooter())
	return b.String()
}

func discordDay(fc forecastResponse, i int) string {
	d := fc.Daily
	date := at(d.Time, i)
	w := wmo(at(d.WeatherCode, i))
	return fmt.Sprintf("**%s %s** %s %s\n🌡️ %.0f–%.0f°C · 🌧️ %.1f mm (%.0f%%) · ☀️ %s · 💨 %.0f km/h (Böen %.0f) %s\n",
		weekday(date), date, w.emoji, w.desc,
		at(d.TempMin, i), at(d.TempMax, i),
		at(d.PrecipSum, i), at(d.PrecipProbMax, i),
		sunHours(at(d.Sunshine, i)),
		at(d.WindMax, i), at(d.GustsMax, i), compass(at(d.WindDir, i)))
}

// buildDiscordForecast renders one block per day, truncated after the last
// complete day that fits into maxLen runes including the footer.
func buildDiscordForecast(p place, fc forecastResponse, maxLen int) string {
	header := discordHeader(p)
	footer := discordFooter()
	hint := truncatedHint + "\n"
	var b strings.Builder
	b.WriteString(header)
	used := len([]rune(header)) + len([]rune(footer))
	for i := range fc.Daily.Time {
		day := discordDay(fc, i)
		n := len([]rune(day))
		reserve := 0
		if i < len(fc.Daily.Time)-1 {
			reserve = len([]rune(hint))
		}
		if used+n+reserve > maxLen {
			b.WriteString(hint)
			break
		}
		b.WriteString(day)
		used += n
	}
	b.WriteString(footer)
	return b.String()
}
