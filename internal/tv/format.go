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

// msgBadSlashDay answers a day choice outside the offered range.
const msgBadSlashDay = "Unknown day: pick today, tomorrow or in 2 … 7 days."

// msgBadDay answers an unusable day option.
const msgBadDay = "Unknown day: use today, tomorrow, yesterday or YYYY-MM-DD (yesterday to +7 days)."

func clock(ts int64, loc *time.Location) string { return time.Unix(ts, 0).In(loc).Format("15:04") }

func dateLabel(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("Mon 2006-01-02")
}

// line renders one programme as "20:15–21:45 · Title: Sub (Category)".
func line(p programme, withDesc bool, loc *time.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s–%s · %s", clock(p.Start, loc), clock(p.Stop, loc), p.Title)
	if p.Sub != "" {
		b.WriteString(": " + p.Sub)
	}
	if len(p.Categories) > 0 {
		b.WriteString(" (" + strings.Join(p.Categories, ", ") + ")")
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
	loc := ix.loc
	lines = append(lines, "**On TV now** ("+clock(t.Unix(), loc)+")")
	for _, ch := range chs {
		cur, next := ix.nowNext(ch, t)
		var parts []string
		switch {
		case cur != nil && compact:
			parts = append(parts, clock(cur.Start, loc)+" "+cur.Title)
		case cur != nil:
			parts = append(parts, line(*cur, withDesc, loc))
		default:
			parts = append(parts, "no data")
		}
		if next != nil {
			parts = append(parts, "then "+clock(next.Start, loc)+" "+next.Title)
		}
		lines = append(lines, "**"+ix.channels[ch].Name+"** "+strings.Join(parts, " · "))
	}
	return capLines(lines, discordLimit)
}

// renderDay lists one channel's programmes on the given day.
func renderDay(ix *index, ch int, date time.Time, withDesc bool) string {
	ps := ix.day(ch, date, ix.loc)
	lines := []string{"**" + ix.channels[ch].Name + "** – " + dateLabel(date, ix.loc)}
	if len(ps) == 0 {
		lines = append(lines, "No listings for this day.")
	}
	for _, p := range ps {
		lines = append(lines, line(p, withDesc, ix.loc))
	}
	return capLines(lines, discordLimit)
}

// renderSearch lists upcoming programmes whose title matches query.
func renderSearch(ix *index, query string, t time.Time, limit int, withDesc bool) string {
	hits := ix.search(query, t, limit)
	if len(hits) == 0 {
		return "No upcoming show with “" + sanitize(query) + "” in its title (search matches titles only; for genres use mode listings)."
	}
	withDesc = withDesc && len(hits) <= descResultLimit
	lines := []string{"**Search:** " + sanitize(query)}
	for _, h := range hits {
		lines = append(lines, "**"+ix.channels[h.ch].Name+"** "+dateLabel(time.Unix(h.p.Start, 0), ix.loc)+" "+line(h.p, withDesc, ix.loc))
	}
	return capLines(lines, discordLimit)
}

// listingsBudget is the rune budget of one listings page, below the tool
// result cap so a page is never cut mid-row.
const listingsBudget = 1900

// genreHint names common feed genres for a category that never matches.
const genreHint = "Drama, Krimi, Action, Komödie, Horror, Abenteuer, Thriller"

// renderListings pages through the programmes matching f, starting at offset.
// rawCategory is the category as the user typed it, for messages.
func renderListings(ix *index, f listingFilter, rawCategory string, now time.Time, offset int) string {
	hits := ix.listings(f, now)
	switch {
	case len(hits) == 0 && f.category != "" && !ix.hasCategory(f.category):
		return "No programme in the feed has the category “" + sanitize(rawCategory) + "”. The feed only carries genres such as " + genreHint + "; it has no film or format field, so try one of those genres instead."
	case len(hits) == 0:
		return "No matching programme starts in that time slot on the selected channels and days."
	case offset >= len(hits):
		return fmt.Sprintf("No more results: offset %d is past the end (%d results).", offset, len(hits))
	}
	header := fmt.Sprintf("**Listings** (%d results, showing from %d):", len(hits), offset+1)
	lines := []string{header}
	n := len([]rune(header))
	i := offset
	for ; i < len(hits); i++ {
		row := listingRow(ix, hits[i])
		add := len([]rune(row)) + 1
		rest := len(hits) - i - 1
		footer := 0
		if rest > 0 {
			footer = len([]rune(moreFooter(rest, i+1))) + 1
		}
		if n+add+footer > listingsBudget {
			break
		}
		lines = append(lines, row)
		n += add
	}
	if i < len(hits) {
		lines = append(lines, moreFooter(len(hits)-i, i))
	}
	return strings.Join(lines, "\n")
}

// moreFooter reports rest omitted rows and the offset of the next page.
func moreFooter(rest, next int) string {
	return fmt.Sprintf("+%d more, use offset=%d", rest, next)
}

// listingRow renders "**Ch** Mon 2026-10-26 20:15 Title (Genre)".
func listingRow(ix *index, h hit) string {
	s := "**" + ix.channels[h.ch].Name + "** " + dateLabel(time.Unix(h.p.Start, 0), ix.loc) + " " + clock(h.p.Start, ix.loc) + " " + h.p.Title
	if len(h.p.Categories) > 0 {
		s += " (" + strings.Join(h.p.Categories, ", ") + ")"
	}
	return s
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
// loc, limited to yesterday through maxDayOffset days ahead. ok is false
// for anything else.
func parseDay(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	now = now.In(loc)
	y, m, d := now.Date()
	today := time.Date(y, m, d, 12, 0, 0, 0, loc)
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
		t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), loc)
		if err != nil {
			return time.Time{}, false
		}
		ty, tm, td := t.Date()
		day = time.Date(ty, tm, td, 12, 0, 0, 0, loc)
	}
	if day.Before(today.AddDate(0, 0, -1)) || day.After(today.AddDate(0, 0, maxDayOffset)) {
		return time.Time{}, false
	}
	return day, true
}
