package tv

import (
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// keepAhead keeps programmes that start at most this far in the future.
	// It exceeds maxDayOffset by two days so an index up to maxIndexAge old
	// still covers the last selectable day. Past programmes are kept from the
	// start of yesterday (Europe/Berlin), the earliest selectable day.
	keepAhead = (maxDayOffset + 2) * 24 * time.Hour
	// maxDescRunes truncates programme descriptions.
	maxDescRunes = 200
)

// placeholderTitles are feed filler rows that carry no programme.
var placeholderTitles = map[string]bool{"no data": true, "sendepause": true, "sendeschluss": true}

var tagPattern = regexp.MustCompile(`<[^>]*>`)

// xmlProgramme is the decoded body of one <programme> element.
type xmlProgramme struct {
	Titles     []string `xml:"title"`
	SubTitles  []string `xml:"sub-title"`
	Categories []string `xml:"category"`
	Descs      []string `xml:"desc"`
}

// xmlChannel is the decoded body of one <channel> element.
type xmlChannel struct {
	DisplayNames []string `xml:"display-name"`
}

// builder accumulates programmes of the allowed channels from one or more
// XMLTV documents (one per region) before the index is built.
type builder struct {
	pos      map[string]int
	channels []channelInfo
	progs    [][]programme
}

// parse builds an index from a single XMLTV document.
func parse(r io.Reader, allow []string, now time.Time, loc *time.Location) (*index, error) {
	b := newBuilder(allow)
	if err := b.add(r); err != nil {
		return nil, err
	}
	return b.build(now, loc), nil
}

// newBuilder prepares the allowed channels in allow order.
func newBuilder(allow []string) *builder {
	pos := make(map[string]int, len(allow))
	channels := make([]channelInfo, 0, len(allow))
	for _, id := range allow {
		if _, dup := pos[id]; dup {
			continue
		}
		pos[id] = len(channels)
		channels = append(channels, channelInfo{ID: id, Name: channelLabel(id, "")})
	}
	return &builder{pos: pos, channels: channels, progs: make([][]programme, len(channels))}
}

// add streams one XMLTV document into the builder.
func (b *builder) add(r io.Reader) error {
	pos, channels, progs := b.pos, b.channels, b.progs
	dec := xml.NewDecoder(r)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("tv: parse epg: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "channel":
			ci, ok := pos[attr(se, "id")]
			if !ok {
				if err := dec.Skip(); err != nil {
					return fmt.Errorf("tv: parse epg: %w", err)
				}
				continue
			}
			var c xmlChannel
			if err := dec.DecodeElement(&c, &se); err != nil {
				return fmt.Errorf("tv: parse epg channel: %w", err)
			}
			if len(c.DisplayNames) > 0 {
				channels[ci].Name = channelLabel(channels[ci].ID, c.DisplayNames[0])
			}
		case "programme":
			ci, ok := pos[attr(se, "channel")]
			if !ok {
				if err := dec.Skip(); err != nil {
					return fmt.Errorf("tv: parse epg: %w", err)
				}
				continue
			}
			var p xmlProgramme
			if err := dec.DecodeElement(&p, &se); err != nil {
				return fmt.Errorf("tv: parse epg programme: %w", err)
			}
			start, ok := parseXMLTVTime(attr(se, "start"))
			if !ok {
				continue
			}
			var stop int64
			if t, ok := parseXMLTVTime(attr(se, "stop")); ok {
				stop = t.Unix()
			}
			title := cleanText(first(p.Titles))
			if placeholderTitles[strings.ToLower(title)] {
				// Kept as an empty title until finalize so it still ends the
				// previous programme when that one has no stop.
				title = ""
			}
			progs[ci] = append(progs[ci], programme{
				Start:      start.Unix(),
				Stop:       stop,
				Title:      title,
				Sub:        cleanText(first(p.SubTitles)),
				Categories: cleanAll(p.Categories),
				Desc:       truncateRunes(cleanText(first(p.Descs)), maxDescRunes),
			})
		}
	}

	return nil
}

// build finalises every channel and returns the index. The kept window starts
// at the beginning of yesterday in loc.
func (b *builder) build(now time.Time, loc *time.Location) *index {
	channels, progs := b.channels, b.progs
	y, mo, d := now.In(loc).Date()
	from := time.Date(y, mo, d-1, 0, 0, 0, 0, loc).Unix()
	to := now.Add(keepAhead).Unix()
	for ci := range progs {
		if shift := primeTimeShift(progs[ci], from, to); shift != 0 {
			slog.Warn("tv: channel clock is shifted, correcting", "channel", channels[ci].ID, "shift", shift)
			shiftProgrammes(progs[ci], shift)
		}
		progs[ci] = finalize(progs[ci], from, to)
		if len(progs[ci]) == 0 {
			slog.Warn("tv: allowlisted channel has no programmes", "channel", channels[ci].ID)
		}
	}
	return newIndex(channels, progs, now, loc)
}

// finalize sorts one channel's rows, drops duplicate starts, fills a missing
// stop from the next start, drops placeholder rows (empty title), and keeps
// only rows overlapping [from, to).
func finalize(ps []programme, from, to int64) []programme {
	// Real rows sort before placeholders with the same start, so the
	// duplicate-start check below keeps the real one.
	sort.SliceStable(ps, func(a, b int) bool {
		if ps[a].Start != ps[b].Start {
			return ps[a].Start < ps[b].Start
		}
		return ps[a].Title != "" && ps[b].Title == ""
	})
	out := ps[:0]
	for i, p := range ps {
		if i > 0 && p.Start == ps[i-1].Start {
			continue
		}
		if p.Stop <= p.Start {
			p.Stop = 0
			for j := i + 1; j < len(ps); j++ {
				if ps[j].Start > p.Start {
					p.Stop = ps[j].Start
					break
				}
			}
			if p.Stop == 0 {
				continue
			}
		}
		if p.Title == "" || p.Stop <= from || p.Start >= to {
			continue
		}
		out = append(out, p)
	}
	return out
}

// parseXMLTVTime parses "20060102150405 -0700"; a value without a zone is UTC.
func parseXMLTVTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("20060102150405 -0700", s); err == nil {
		return t, true
	}
	if len(s) >= 14 {
		if t, err := time.ParseInLocation("20060102150405", s[:14], time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func attr(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// cleanText strips HTML tags, unescapes entities and collapses whitespace.
func cleanText(s string) string {
	s = html.UnescapeString(tagPattern.ReplaceAllString(s, " "))
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// cleanAll cleans every value and drops empty ones.
func cleanAll(vs []string) []string {
	var out []string
	for _, v := range vs {
		if c := cleanText(v); c != "" {
			out = append(out, c)
		}
	}
	return out
}
