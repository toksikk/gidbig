package tv

import (
	"fmt"
	"strings"
	"time"
)

const (
	// maxWindowHours caps every /tv time window.
	maxWindowHours = 6
	// maxReplyLines caps listed shows per /tv reply; the rest is counted.
	maxReplyLines = 20
	// maxSearchHits caps /tv search results.
	maxSearchHits = 10
	// footer credits the data source under every listing.
	footer = "-# Data: free-epg.de · times in your local time zone"
)

// ts renders a Discord timestamp, shown in each reader's own time zone.
func ts(unix int64, style string) string { return fmt.Sprintf("<t:%d:%s>", unix, style) }

// nowTitleRunes and nextTitleRunes shorten titles in the all-channels view.
const (
	nowTitleRunes  = 40
	nextTitleRunes = 28
)

// mdEscaper keeps feed text from toggling Discord markdown.
var mdEscaper = strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`", "~", "\\~", "|", "\\|", "<", "\\<")

func md(s string) string { return mdEscaper.Replace(s) }

// showText is "Title · Sub *Category*" without times; bold marks the title.
func showText(p programme, bold bool) string {
	s := md(p.Title)
	if bold {
		s = "**" + s + "**"
	}
	if p.Sub != "" {
		s += " · " + md(p.Sub)
	}
	if p.Category != "" {
		s += " *" + md(p.Category) + "*"
	}
	return s
}

// moreLine reports n omitted entries.
func moreLine(n int, hint string) string {
	return fmt.Sprintf("-# +%d more%s", n, hint)
}

// discordNowAll lists every channel's running and next show, one line each.
// It is the one view not capped at maxReplyLines; only the message limit
// bounds it.
func discordNowAll(ix *index, t time.Time) string {
	lines := []string{"📺 **On TV now**"}
	for ch := range ix.channels {
		cur, next := ix.nowNext(ch, t)
		l := "**" + ix.channels[ch].Name + "** "
		if cur != nil {
			l += md(truncateRunes(cur.Title, nowTitleRunes))
		} else {
			l += "–"
		}
		if next != nil {
			l += " → " + ts(next.Start, "t") + " " + md(truncateRunes(next.Title, nextTitleRunes))
		}
		lines = append(lines, l)
	}
	return withFooter(lines)
}

// withFooter joins lines and the footer below the Discord limit. Lines that
// do not fit are dropped from the end and replaced by a "+N more" line.
func withFooter(lines []string) string {
	budget := discordLimit - len([]rune(footer)) - 1
	n := 0
	for i, l := range lines {
		cost := len([]rune(l)) + 1
		if n+cost > budget-len([]rune(moreLine(len(lines), "")))-1 && i > 0 {
			kept := append(lines[:i:i], moreLine(len(lines)-i, ""))
			return strings.Join(kept, "\n") + "\n" + footer
		}
		n += cost
	}
	return strings.Join(lines, "\n") + "\n" + footer
}

// discordWindow lists one channel's shows overlapping [from, from+hours).
// A show already running at from is marked with ▶.
func discordWindow(ix *index, ch int, from time.Time, hours int) string {
	to := from.Add(time.Duration(hours) * time.Hour)
	ps := ix.window(ch, from.Unix(), to.Unix())
	head := fmt.Sprintf("📺 **%s** · %s – %s", ix.channels[ch].Name, ts(from.Unix(), "f"), ts(to.Unix(), "t"))
	lines := []string{head}
	if len(ps) == 0 {
		lines = append(lines, "No listings in this time span.")
	}
	shown := ps
	if len(shown) > maxReplyLines {
		shown = shown[:maxReplyLines]
	}
	for _, p := range shown {
		mark := ""
		if p.Start < from.Unix() {
			mark = "▶ "
		}
		lines = append(lines, mark+ts(p.Start, "t")+" "+showText(p, true))
	}
	if n := len(ps) - len(shown); n > 0 {
		lines = append(lines, moreLine(n, " – pick a shorter `hours`"))
	}
	return withFooter(lines)
}

// discordSearch lists upcoming shows whose title matches query.
func discordSearch(ix *index, query string, t time.Time) string {
	// One extra hit tells whether more exist without collecting them all.
	hits := ix.search(query, t, maxSearchHits+1)
	if len(hits) == 0 {
		return "No upcoming show found for “" + sanitize(query) + "”."
	}
	lines := []string{"🔎 **" + sanitize(query) + "**"}
	shown := hits
	if len(shown) > maxSearchHits {
		shown = shown[:maxSearchHits]
	}
	for _, h := range shown {
		lines = append(lines, ts(h.p.Start, "f")+" · **"+ix.channels[h.ch].Name+"** · "+showText(h.p, false))
	}
	if len(hits) > len(shown) {
		lines = append(lines, "-# more results – refine the title")
	}
	return withFooter(lines)
}
