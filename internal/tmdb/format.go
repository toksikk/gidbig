package tmdb

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const (
	// Attribution is required by TMDB's terms: JustWatch must be credited for
	// watch-provider data, and the TMDB notice must be shown.
	Attribution = "Data by JustWatch via TMDB · This product uses the TMDB API but is not endorsed or certified by TMDB."

	// maxOptions is Discord's select-menu option limit.
	maxOptions = 25
	// maxLabelRunes is Discord's select-menu option label limit.
	maxLabelRunes = 100

	tmdbSiteURL = "https://www.themoviedb.org"
)

// category is one shown offer type with its German embed label.
type category struct {
	label string
	pick  func(RegionProviders) []Provider
}

// shownCategories lists the offer types the module displays. Rent and buy are
// intentionally hidden.
var shownCategories = []category{
	{label: "Streaming (Abo)", pick: func(r RegionProviders) []Provider { return r.Flatrate }},
	{label: "Kostenlos", pick: func(r RegionProviders) []Provider { return r.Free }},
	{label: "Mit Werbung", pick: func(r RegionProviders) []Provider { return r.Ads }},
}

// providerNames sorts by display priority and joins the names.
func providerNames(ps []Provider) string {
	sorted := append([]Provider(nil), ps...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].Priority < sorted[b].Priority })
	names := make([]string, 0, len(sorted))
	for _, p := range sorted {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

// kindLabel is the German label for a media kind.
func kindLabel(kind string) string {
	if kind == kindTV {
		return "Serie"
	}
	return "Film"
}

// displayTitle renders "Title (Year)".
func displayTitle(m Match) string {
	if m.Year == "" {
		return m.Title
	}
	return m.Title + " (" + m.Year + ")"
}

// optionLabel renders a select-menu label within Discord's limit.
func optionLabel(m Match) string {
	return truncateRunes(displayTitle(m)+" · "+kindLabel(m.Kind), maxLabelRunes)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// optionValue encodes a match as "kind:id".
func optionValue(m Match) string { return m.Kind + ":" + strconv.Itoa(m.ID) }

// selectOptions turns matches into at most maxOptions menu options.
func selectOptions(matches []Match) []discordgo.SelectMenuOption {
	if len(matches) > maxOptions {
		matches = matches[:maxOptions]
	}
	opts := make([]discordgo.SelectMenuOption, 0, len(matches))
	for _, m := range matches {
		opts = append(opts, discordgo.SelectMenuOption{Label: optionLabel(m), Value: optionValue(m)})
	}
	return opts
}

// watchURL is the TMDB watch page; TMDB's own link wins when present.
func watchURL(d Details, rp RegionProviders, ok bool) string {
	if ok && rp.Link != "" {
		return rp.Link
	}
	return fmt.Sprintf("%s/%s/%d/watch", tmdbSiteURL, d.Kind, d.ID)
}

// buildEmbed renders the provider list for one region.
func buildEmbed(d Details, region string) *discordgo.MessageEmbed {
	rp, ok := d.Regions[region]
	e := &discordgo.MessageEmbed{
		Title:  truncateRunes(displayTitle(d.Match), 256),
		URL:    watchURL(d, rp, ok),
		Footer: &discordgo.MessageEmbedFooter{Text: Attribution},
	}
	if !ok {
		e.Description = fmt.Sprintf("No streaming data for %s in %s.", displayTitle(d.Match), region)
		return e
	}
	for _, c := range shownCategories {
		if names := providerNames(c.pick(rp)); names != "" {
			e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: c.label, Value: truncateRunes(names, 1024)})
		}
	}
	if len(e.Fields) == 0 {
		e.Description = fmt.Sprintf("Not available to stream in %s (rent/buy may exist on TMDB).", region)
	} else {
		e.Description = "Region: " + region
	}
	return e
}

// formatText renders the tool result as plain text for the model.
func formatText(d Details, region string) string {
	rp, ok := d.Regions[region]
	var b strings.Builder
	fmt.Fprintf(&b, "Match: %s [%s, TMDB id %d]\nRegion: %s\n", displayTitle(d.Match), d.Kind, d.ID, region)
	if !ok {
		b.WriteString("No streaming data for this region.\n")
	} else {
		found := false
		for _, c := range []struct {
			name string
			ps   []Provider
		}{
			{"Subscription (flatrate)", rp.Flatrate},
			{"Free", rp.Free},
			{"With ads", rp.Ads},
		} {
			if names := providerNames(c.ps); names != "" {
				fmt.Fprintf(&b, "%s: %s\n", c.name, names)
				found = true
			}
		}
		if !found {
			b.WriteString("Not available to stream (rent/buy may exist).\n")
		}
	}
	fmt.Fprintf(&b, "Link: %s\n%s", watchURL(d, rp, ok), Attribution)
	return b.String()
}
