package openholidays

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	dateLayout     = "2006-01-02"
	maxMessageLen  = 2000
	attribution    = "-# Data: OpenHolidays API (ODbL)"
	noHolidaysText = "No holidays found."
)

// pickName returns the German name, else English, else the first one.
func pickName(names []localizedText) string {
	for _, lang := range []string{"DE", "EN"} {
		for _, n := range names {
			if strings.EqualFold(n.Language, lang) && n.Text != "" {
				return n.Text
			}
		}
	}
	for _, n := range names {
		if n.Text != "" {
			return n.Text
		}
	}
	return "?"
}

// sortHolidays orders holidays by start date, then name.
func sortHolidays(hs []holiday) {
	sort.SliceStable(hs, func(a, b int) bool {
		if hs[a].StartDate != hs[b].StartDate {
			return hs[a].StartDate < hs[b].StartDate
		}
		return pickName(hs[a].Name) < pickName(hs[b].Name)
	})
}

// filterRegion keeps nationwide holidays and those valid in region (or one of
// its children). An empty region keeps everything.
func filterRegion(hs []holiday, region string) []holiday {
	if region == "" {
		return hs
	}
	var out []holiday
	for _, h := range hs {
		if h.Nationwide || len(h.Subdivisions) == 0 {
			out = append(out, h)
			continue
		}
		for _, s := range h.Subdivisions {
			if s.Code == region || strings.HasPrefix(s.Code, region+"-") || strings.HasPrefix(region, s.Code+"-") {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

func weekday(date string) string {
	t, err := time.Parse(dateLayout, date)
	if err != nil {
		return "???"
	}
	return t.Format("Mon")
}

// spanDays returns the inclusive number of days between start and end.
func spanDays(start, end string) int {
	s, err1 := time.Parse(dateLayout, start)
	e, err2 := time.Parse(dateLayout, end)
	if err1 != nil || err2 != nil || e.Before(s) {
		return 1
	}
	return int(e.Sub(s).Hours()/24) + 1
}

// regionTags lists subdivision short names (or codes) of a regional holiday.
func regionTags(h holiday) string {
	if h.Nationwide || len(h.Subdivisions) == 0 {
		return ""
	}
	tags := make([]string, 0, len(h.Subdivisions))
	for _, s := range h.Subdivisions {
		if s.ShortName != "" {
			tags = append(tags, s.ShortName)
		} else {
			tags = append(tags, s.Code)
		}
	}
	return strings.Join(tags, ", ")
}

// discordLine renders one holiday for the slash reply.
func discordLine(h holiday, kind string) string {
	var b strings.Builder
	if kind == typeSchool || h.EndDate != h.StartDate {
		fmt.Fprintf(&b, "%s %s → %s %s (%d days)  ", weekday(h.StartDate), h.StartDate, weekday(h.EndDate), h.EndDate, spanDays(h.StartDate, h.EndDate))
	} else {
		fmt.Fprintf(&b, "%s %s  ", weekday(h.StartDate), h.StartDate)
	}
	b.WriteString(pickName(h.Name))
	if tags := regionTags(h); tags != "" {
		b.WriteString(" (" + tags + ")")
	}
	return strings.ReplaceAll(b.String(), "`", "'")
}

// buildDiscord renders the /holidays reply, capped at maxMessageLen.
func buildDiscord(q query, hs []holiday) string {
	label := "🎉 Public holidays"
	if q.kind == typeSchool {
		label = "🏫 School holidays"
	}
	where := q.country
	if q.region != "" {
		where = q.region
	}
	header := fmt.Sprintf("## %s %s · %s → %s\n", label, where, q.from, q.to)
	footer := "\n" + attribution
	if len(hs) == 0 {
		return header + noHolidaysText + footer
	}
	const open, closeBlock = "```\n", "```"
	budget := maxMessageLen - len(header) - len(footer) - len(open) - len(closeBlock)
	var body strings.Builder
	for idx, h := range hs {
		line := discordLine(h, q.kind) + "\n"
		rest := len(hs) - idx - 1
		reserve := 0
		if rest > 0 {
			reserve = len(moreLine(rest)) + 8
		}
		if utf8.RuneCountInString(body.String())+utf8.RuneCountInString(line)+reserve > budget {
			body.WriteString(moreLine(len(hs) - idx))
			break
		}
		body.WriteString(line)
	}
	return header + open + body.String() + closeBlock + footer
}

func moreLine(n int) string { return fmt.Sprintf("…and %d more\n", n) }

// buildToolText renders plain text for the LLM tool.
func buildToolText(q query, hs []holiday) string {
	var b strings.Builder
	where := q.country
	if q.region != "" {
		where = q.region
	}
	fmt.Fprintf(&b, "%s holidays in %s from %s to %s (source: OpenHolidays API, ODbL)\n", q.kind, where, q.from, q.to)
	if len(hs) == 0 {
		b.WriteString(noHolidaysText + "\n")
		return b.String()
	}
	for _, h := range hs {
		scope := "nationwide"
		if tags := regionTags(h); tags != "" {
			scope = "regions: " + tags
		}
		if h.EndDate != h.StartDate {
			fmt.Fprintf(&b, "%s (%s) to %s (%s): %s; %s; %s\n", h.StartDate, weekday(h.StartDate), h.EndDate, weekday(h.EndDate), pickName(h.Name), scope, strings.ToLower(h.Type))
		} else {
			fmt.Fprintf(&b, "%s (%s): %s; %s; %s\n", h.StartDate, weekday(h.StartDate), pickName(h.Name), scope, strings.ToLower(h.Type))
		}
	}
	return b.String()
}
