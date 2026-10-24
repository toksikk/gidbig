package tv

import (
	"sort"
	"strings"
	"time"
)

// programme is one broadcast slot. Start and Stop are Unix seconds; the slot
// covers the half-open interval [Start, Stop).
type programme struct {
	Start, Stop int64
	Title       string
	Sub         string
	Category    string
	Desc        string
}

// channelInfo identifies one indexed channel.
type channelInfo struct {
	ID   string
	Name string
}

// titleKey points at one programme by its folded title for search.
type titleKey struct {
	norm string
	ch   int
	i    int
}

// hit is one programme together with its channel position.
type hit struct {
	ch int
	p  programme
}

// index is an immutable in-memory view of the feed. It is replaced as a whole
// through an atomic pointer, so readers never lock.
type index struct {
	channels []channelInfo
	progs    [][]programme // parallel to channels, sorted by Start
	aliases  map[string]int
	titles   []titleKey
	builtAt  time.Time
	loc      *time.Location // display zone and day boundaries
}

func newIndex(channels []channelInfo, progs [][]programme, now time.Time, loc *time.Location) *index {
	ix := &index{channels: channels, progs: progs, aliases: make(map[string]int), builtAt: now, loc: loc}
	for ci, c := range channels {
		names := append([]string{c.ID, trimRegion(c.ID), c.Name}, builtinAliases[c.ID]...)
		for _, n := range names {
			if k := foldKey(n); k != "" {
				if _, taken := ix.aliases[k]; !taken {
					ix.aliases[k] = ci
				}
			}
		}
		for i, p := range progs[ci] {
			ix.titles = append(ix.titles, titleKey{norm: foldText(p.Title + " " + p.Sub), ch: ci, i: i})
		}
	}
	return ix
}

// programmeCount returns the number of indexed programmes.
func (ix *index) programmeCount() int { return len(ix.titles) }

// resolveChannel maps a user-supplied channel name to its position.
func (ix *index) resolveChannel(name string) (int, bool) {
	ci, ok := ix.aliases[foldKey(name)]
	return ci, ok
}

// suggest returns up to limit channel positions whose folded aliases or names
// contain the folded input, in channel order. Empty input matches every channel.
func (ix *index) suggest(input string, limit int) []int {
	k := foldKey(input)
	var out []int
	for ci, c := range ix.channels {
		match := k == "" || strings.Contains(foldKey(c.Name), k) || strings.Contains(foldKey(c.ID), k)
		if !match {
			for alias, ai := range ix.aliases {
				if ai == ci && strings.Contains(alias, k) {
					match = true
					break
				}
			}
		}
		if match {
			out = append(out, ci)
			if len(out) == limit {
				break
			}
		}
	}
	return out
}

// nowNext returns the programme running at t and the one after it.
func (ix *index) nowNext(ch int, t time.Time) (cur, next *programme) {
	ps := ix.progs[ch]
	ts := t.Unix()
	i := sort.Search(len(ps), func(i int) bool { return ps[i].Start > ts })
	if i > 0 && ts < ps[i-1].Stop {
		cur = &ps[i-1]
	}
	if i < len(ps) {
		next = &ps[i]
	}
	return cur, next
}

// day returns the programmes overlapping the local calendar day of date in loc.
func (ix *index) day(ch int, date time.Time, loc *time.Location) []programme {
	y, m, d := date.In(loc).Date()
	from := time.Date(y, m, d, 0, 0, 0, 0, loc).Unix()
	to := time.Date(y, m, d+1, 0, 0, 0, 0, loc).Unix()
	ps := ix.progs[ch]
	i := sort.Search(len(ps), func(i int) bool { return ps[i].Stop > from })
	j := sort.Search(len(ps), func(j int) bool { return ps[j].Start >= to })
	if i >= j {
		return nil
	}
	return ps[i:j]
}

// search finds programmes whose folded title or subtitle contains the folded
// query and that have not ended before from, ordered by start, at most limit.
func (ix *index) search(query string, from time.Time, limit int) []hit {
	q := foldText(query)
	if q == "" {
		return nil
	}
	fs := from.Unix()
	var out []hit
	for _, tk := range ix.titles {
		p := ix.progs[tk.ch][tk.i]
		if p.Stop > fs && strings.Contains(tk.norm, q) {
			out = append(out, hit{ch: tk.ch, p: p})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].p.Start < out[b].p.Start })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
