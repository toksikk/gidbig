package openmeteo

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	discordMaxLen = 2000
	detailDays    = 3
	attribution   = "Data: Open-Meteo.com (CC BY 4.0)"
	maxBlockDays  = 7
	// tableRow lays out the compact forecast columns, right-aligned.
	tableRow = "%-9s  %7s  %7s %4s  %5s  %7s"
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

// discordHeader mirrors wttrin's location heading, including the maps link.
func discordHeader(p place) string {
	label := p.Name
	if p.Country != "" {
		label += ", " + p.Country
	}
	if p.Admin1 != "" && p.Admin1 != p.Name {
		label += " (" + p.Admin1 + ")"
	}
	return fmt.Sprintf("## 📍 [%s](<https://www.google.com/maps/place/%.2f,%.2f>)\n", label, p.Latitude, p.Longitude)
}

func discordFooter() string {
	return "-# " + attribution
}

// capitalize upper-cases the first letter of a WMO description for display.
func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// windArrow maps a wind direction in degrees to wttrin's arrow emojis.
func windArrow(deg float64) string {
	d := math.Mod(math.Round(deg), 360)
	if d < 0 {
		d += 360
	}
	switch {
	case d >= 337 || d <= 22:
		return "⬆️"
	case d <= 67:
		return "↗️"
	case d <= 112:
		return "➡️"
	case d <= 157:
		return "↘️"
	case d <= 202:
		return "⬇️"
	case d <= 247:
		return "↙️"
	case d <= 292:
		return "⬅️"
	default:
		return "↖️"
	}
}

// warnLine lists notable risks for day i, like wttrin's high-chance line.
// It returns "" when nothing applies.
func warnLine(fc forecastResponse, i int) string {
	d := fc.Daily
	var parts []string
	if p := at(d.PrecipProbMax, i); p > 50 {
		parts = append(parts, fmt.Sprintf("🌧️ %.0f%%", p))
	}
	if g := at(d.GustsMax, i); g >= 50 {
		parts = append(parts, fmt.Sprintf("💨 %.0fkm/h", g))
	}
	if c := at(d.WeatherCode, i); c >= 95 && c <= 99 {
		parts = append(parts, "⛈️")
	}
	if at(d.SnowSum, i) > 0 {
		parts = append(parts, "❄️")
	}
	if len(parts) == 0 {
		return ""
	}
	return "⚠️ " + strings.Join(parts, " ")
}

// buildDiscordCurrent renders current conditions plus a today line for /weather.
func buildDiscordCurrent(p place, fc forecastResponse) string {
	c := fc.Current
	w := wmo(c.WeatherCode)
	var b strings.Builder
	b.WriteString(discordHeader(p))
	b.WriteString("```\n")
	fmt.Fprintf(&b, "🌡️ %.0f°C (feels like %.0f°C)\n", c.Temperature, c.Apparent)
	fmt.Fprintf(&b, "💧 %.0f%% humidity\n", c.Humidity)
	fmt.Fprintf(&b, "🌬️ %s %.0fkm/h\n", windArrow(c.WindDirection), c.WindSpeed)
	fmt.Fprintf(&b, "%s %s\n", w.emoji, capitalize(w.desc))
	if len(fc.Daily.Time) > 0 {
		d := fc.Daily
		fmt.Fprintf(&b, "📅 Today: %.0f°C / %.0f°C, 🌧️ %.1fmm (%.0f%%), ☀️ %s\n",
			at(d.TempMax, 0), at(d.TempMin, 0), at(d.PrecipSum, 0), at(d.PrecipProbMax, 0), sunHours(at(d.Sunshine, 0)))
		if warn := warnLine(fc, 0); warn != "" {
			b.WriteString(warn + "\n")
		}
	}
	b.WriteString("```\n")
	b.WriteString(discordFooter())
	return b.String()
}

// discordDayBlock renders one wttrin-style forecast day.
func discordDayBlock(fc forecastResponse, i int) string {
	d := fc.Daily
	date := at(d.Time, i)
	w := wmo(at(d.WeatherCode, i))
	var b strings.Builder
	fmt.Fprintf(&b, "### 📅 %s %s\n```\n", weekday(date), date)
	fmt.Fprintf(&b, "🌡️ %.0f°C / %.0f°C\n", at(d.TempMax, i), at(d.TempMin, i))
	fmt.Fprintf(&b, "🌬️ %s %.0fkm/h (gusts %.0fkm/h)\n", windArrow(at(d.WindDir, i)), at(d.WindMax, i), at(d.GustsMax, i))
	fmt.Fprintf(&b, "%s %s\n", w.emoji, capitalize(w.desc))
	if rain := at(d.PrecipSum, i); rain > 0 {
		fmt.Fprintf(&b, "🌧️ %.1fmm (%.0f%%)\n", rain, at(d.PrecipProbMax, i))
	}
	if snow := at(d.SnowSum, i); snow > 0 {
		fmt.Fprintf(&b, "❄️ %.1fcm\n", snow)
	}
	fmt.Fprintf(&b, "☀️ %s sun\n", sunHours(at(d.Sunshine, i)))
	if warn := warnLine(fc, i); warn != "" {
		b.WriteString(warn + "\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// buildDiscordTable renders one aligned line per day for long forecasts.
// Emojis sit at the line end so the monospace columns stay aligned.
func buildDiscordTable(fc forecastResponse) string {
	d := fc.Daily
	var b strings.Builder
	b.WriteString("```\n")
	fmt.Fprintf(&b, tableRow+"\n", "Day", "°C", "Rain", "%", "Sun", "Wind")
	for i, date := range d.Time {
		short := date
		if len(date) == len("2006-01-02") {
			short = date[5:]
		}
		w := wmo(at(d.WeatherCode, i))
		fmt.Fprintf(&b, tableRow+" %s %s\n",
			weekday(date)+" "+short,
			fmt.Sprintf("%.0f/%.0f", at(d.TempMax, i), at(d.TempMin, i)),
			fmt.Sprintf("%.1fmm", at(d.PrecipSum, i)),
			fmt.Sprintf("%.0f%%", at(d.PrecipProbMax, i)),
			sunHours(at(d.Sunshine, i)),
			fmt.Sprintf("%.0fkm/h", at(d.WindMax, i)),
			windArrow(at(d.WindDir, i)), w.emoji)
	}
	b.WriteString("```\n")
	return b.String()
}

// buildDiscordForecast renders wttrin-style day blocks for up to
// maxBlockDays days and a compact table for longer forecasts.
func buildDiscordForecast(p place, fc forecastResponse) string {
	var b strings.Builder
	b.WriteString(discordHeader(p))
	if len(fc.Daily.Time) <= maxBlockDays {
		for i := range fc.Daily.Time {
			b.WriteString(discordDayBlock(fc, i))
		}
	} else {
		b.WriteString(buildDiscordTable(fc))
	}
	b.WriteString(discordFooter())
	return b.String()
}
