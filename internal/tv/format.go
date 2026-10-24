package tv

import (
	"fmt"
	"strings"
	"time"
)

const (
	// discordLimit is Discord's message content limit.
	discordLimit = 2000
	// searchLimit caps search results.
	searchLimit = 15
	// descResultLimit is the most results that still carry descriptions.
	descResultLimit = 10
	// maxDayOffset is how many days ahead /tv channel accepts.
	maxDayOffset = 7
)

var weekdays = [...]string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}

// msgBadDay answers an unusable day option.
const msgBadDay = "Tag unbekannt: nutze heute, morgen, übermorgen oder JJJJ-MM-TT (gestern bis +7 Tage)."

func clock(ts int64) string { return time.Unix(ts, 0).In(berlin).Format("15:04") }

func dateLabel(t time.Time) string {
	t = t.In(berlin)
	return weekdays[t.Weekday()] + " " + t.Format("02.01.2006")
}

// line renders one programme as "20:15–21:45 · Title: Sub (Category)".
func line(p programme, withDesc bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s–%s · %s", clock(p.Start), clock(p.Stop), p.Title)
	if p.Sub != "" {
		b.WriteString(": " + p.Sub)
	}
	if p.Category != "" {
		b.WriteString(" (" + p.Category + ")")
	}
	if withDesc && p.Desc != "" {
		b.WriteString("\n  " + p.Desc)
	}
	return b.String()
}

// renderNow lists the running and the next programme for each channel. With
// more than one channel each line is shortened to start time and title so the
// full channel list fits one Discord message.
func renderNow(ix *index, chs []int, t time.Time, withDesc bool) string {
	compact := len(chs) > 1
	lines := make([]string, 0, len(chs)+1)
	lines = append(lines, "**Jetzt im TV** ("+clock(t.Unix())+")")
	for _, ch := range chs {
		cur, next := ix.nowNext(ch, t)
		var parts []string
		switch {
		case cur != nil && compact:
			parts = append(parts, clock(cur.Start)+" "+cur.Title)
		case cur != nil:
			parts = append(parts, line(*cur, withDesc))
		default:
			parts = append(parts, "keine Daten")
		}
		if next != nil {
			parts = append(parts, "danach "+clock(next.Start)+" "+next.Title)
		}
		lines = append(lines, "**"+ix.channels[ch].Name+"** "+strings.Join(parts, " · "))
	}
	return capLines(lines, discordLimit)
}

// renderDay lists one channel's programmes on the given day.
func renderDay(ix *index, ch int, date time.Time, withDesc bool) string {
	ps := ix.day(ch, date, berlin)
	lines := []string{"**" + ix.channels[ch].Name + "** – " + dateLabel(date)}
	if len(ps) == 0 {
		lines = append(lines, "Keine Programmdaten für diesen Tag.")
	}
	for _, p := range ps {
		lines = append(lines, line(p, withDesc))
	}
	return capLines(lines, discordLimit)
}

// renderSearch lists upcoming programmes whose title matches query.
func renderSearch(ix *index, query string, t time.Time, limit int, withDesc bool) string {
	hits := ix.search(query, t, limit)
	if len(hits) == 0 {
		return "Keine Sendung gefunden für „" + sanitize(query) + "“."
	}
	withDesc = withDesc && len(hits) <= descResultLimit
	lines := []string{"**Suche:** " + sanitize(query)}
	for _, h := range hits {
		lines = append(lines, "**"+ix.channels[h.ch].Name+"** "+dateLabel(time.Unix(h.p.Start, 0))+" "+line(h.p, withDesc))
	}
	return capLines(lines, discordLimit)
}

// capLines joins lines and drops trailing ones so the text fits limit runes.
func capLines(lines []string, limit int) string {
	const more = "…"
	n := 0
	for i, l := range lines {
		add := len([]rune(l))
		if i > 0 {
			add++
		}
		if n+add > limit-len([]rune(more))-1 {
			return strings.Join(lines[:i], "\n") + "\n" + more
		}
		n += add
	}
	return strings.Join(lines, "\n")
}

// sanitize keeps user input from breaking Discord formatting.
func sanitize(s string) string {
	s = strings.NewReplacer("`", "'", "*", "", "_", " ", "\n", " ").Replace(strings.TrimSpace(s))
	return truncateRunes(s, 100)
}

// parseDay maps heute/morgen/übermorgen/gestern or YYYY-MM-DD to a date in
// berlin, limited to yesterday through maxDayOffset days ahead. ok is false
// for anything else.
func parseDay(s string, now time.Time) (time.Time, bool) {
	now = now.In(berlin)
	y, m, d := now.Date()
	today := time.Date(y, m, d, 12, 0, 0, 0, berlin)
	var day time.Time
	switch foldKey(s) {
	case "", "heute", "today":
		return today, true
	case "morgen", "tomorrow":
		return today.AddDate(0, 0, 1), true
	case "uebermorgen":
		return today.AddDate(0, 0, 2), true
	case "gestern", "yesterday":
		return today.AddDate(0, 0, -1), true
	default:
		t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), berlin)
		if err != nil {
			return time.Time{}, false
		}
		ty, tm, td := t.Date()
		day = time.Date(ty, tm, td, 12, 0, 0, 0, berlin)
	}
	if day.Before(today.AddDate(0, 0, -1)) || day.After(today.AddDate(0, 0, maxDayOffset)) {
		return time.Time{}, false
	}
	return day, true
}
