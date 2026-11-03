package react

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// pageSize is Discord's select-menu option limit.
const pageSize = 25

// collectEmojis returns the available custom emojis across guilds, deduplicated
// by ID, filtered by a case-insensitive name substring, sorted by name then ID.
func collectEmojis(guilds []*discordgo.Guild, filter string) []*discordgo.Emoji {
	filter = strings.ToLower(strings.TrimSpace(filter))
	seen := make(map[string]bool)
	var out []*discordgo.Emoji
	for _, g := range guilds {
		if g == nil {
			continue
		}
		for _, e := range g.Emojis {
			if e == nil || e.ID == "" || !e.Available || seen[e.ID] {
				continue
			}
			if filter != "" && !strings.Contains(strings.ToLower(e.Name), filter) {
				continue
			}
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func pageCount(n int) int {
	if n <= 0 {
		return 1
	}
	return (n + pageSize - 1) / pageSize
}

func clampPage(page, n int) int {
	if page < 0 {
		return 0
	}
	if last := pageCount(n) - 1; page > last {
		return last
	}
	return page
}

// buildPage renders one picker page: a select menu plus Prev/Next buttons.
func buildPage(tok string, emojis []*discordgo.Emoji, page int) []discordgo.MessageComponent {
	page = clampPage(page, len(emojis))
	pages := pageCount(len(emojis))
	start := page * pageSize
	end := min(start+pageSize, len(emojis))

	opts := make([]discordgo.SelectMenuOption, 0, end-start)
	for _, e := range emojis[start:end] {
		opts = append(opts, discordgo.SelectMenuOption{
			Label: e.Name,
			Value: e.ID,
			Emoji: &discordgo.ComponentEmoji{ID: e.ID, Name: e.Name, Animated: e.Animated},
		})
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    "react:sel:" + tok,
				Placeholder: fmt.Sprintf("Pick an emoji (page %d/%d)", page+1, pages),
				Options:     opts,
			},
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Prev",
				Style:    discordgo.SecondaryButton,
				CustomID: fmt.Sprintf("react:prev:%s:%d", tok, page-1),
				Disabled: page == 0,
			},
			discordgo.Button{
				Label:    fmt.Sprintf("Page %d/%d", page+1, pages),
				Style:    discordgo.SecondaryButton,
				CustomID: "react:page:" + tok,
				Disabled: true,
			},
			discordgo.Button{
				Label:    "Next",
				Style:    discordgo.SecondaryButton,
				CustomID: fmt.Sprintf("react:next:%s:%d", tok, page+1),
				Disabled: page >= pages-1,
			},
		}},
	}
}
