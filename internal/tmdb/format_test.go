package tmdb

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func darkDetails() Details {
	return Details{
		Match: Match{Kind: kindTV, ID: 70523, Title: "Dark", Year: "2017"},
		Regions: map[string]RegionProviders{"DE": {
			Link:     "https://www.themoviedb.org/tv/70523/watch?locale=DE",
			Flatrate: []Provider{{Name: "Amazon", Priority: 5}, {Name: "Netflix", Priority: 1}},
			Ads:      []Provider{{Name: "Pluto", Priority: 2}},
			Rent:     []Provider{{Name: "Apple TV", Priority: 1}},
			Buy:      []Provider{{Name: "Google Play", Priority: 1}},
		}},
	}
}

func TestBuildEmbed_ShownCategoriesSortedWithAttribution(t *testing.T) {
	e := buildEmbed(darkDetails(), "DE")
	if e.Title != "Dark (2017)" || e.URL != "https://www.themoviedb.org/tv/70523/watch?locale=DE" {
		t.Errorf("title/url = %q %q", e.Title, e.URL)
	}
	if len(e.Fields) != 2 {
		t.Fatalf("fields = %+v, want flatrate and ads only", e.Fields)
	}
	if e.Fields[0].Name != "Streaming (Abo)" || e.Fields[0].Value != "Netflix, Amazon" {
		t.Errorf("flatrate field = %+v", e.Fields[0])
	}
	if e.Fields[1].Name != "Mit Werbung" || e.Fields[1].Value != "Pluto" {
		t.Errorf("ads field = %+v", e.Fields[1])
	}
	for _, f := range e.Fields {
		if strings.Contains(f.Value, "Apple TV") || strings.Contains(f.Value, "Google Play") {
			t.Errorf("rent/buy leaked: %+v", f)
		}
	}
	if e.Footer == nil || !strings.Contains(e.Footer.Text, "JustWatch") || !strings.Contains(e.Footer.Text, "not endorsed or certified by TMDB") {
		t.Errorf("footer = %+v, want attribution", e.Footer)
	}
}

func TestBuildEmbed_MissingRegionAndEmpty(t *testing.T) {
	e := buildEmbed(darkDetails(), "US")
	if !strings.Contains(e.Description, "No streaming data for Dark (2017) in US") {
		t.Errorf("missing region description = %q", e.Description)
	}
	if e.URL != "https://www.themoviedb.org/tv/70523/watch" || e.Footer == nil {
		t.Errorf("fallback url/footer = %q %+v", e.URL, e.Footer)
	}

	d := darkDetails()
	d.Regions["AT"] = RegionProviders{Rent: []Provider{{Name: "Apple TV"}}}
	e = buildEmbed(d, "AT")
	if len(e.Fields) != 0 || !strings.Contains(e.Description, "Not available to stream in AT") {
		t.Errorf("rent-only embed = %+v", e)
	}
}

func TestFormatText(t *testing.T) {
	got := formatText(darkDetails(), "DE")
	for _, want := range []string{"Dark (2017)", "[tv, TMDB id 70523]", "Region: DE", "Netflix, Amazon", "With ads: Pluto", "JustWatch", "https://www.themoviedb.org/tv/70523/watch?locale=DE"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatText missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "Apple TV") {
		t.Errorf("rent leaked into %q", got)
	}
}

func TestSelectOptions_TruncatesAndCaps(t *testing.T) {
	matches := make([]Match, 30)
	for i := range matches {
		matches[i] = Match{Kind: kindMovie, ID: i + 1, Title: strings.Repeat("ä", 150), Year: "2001"}
	}
	opts := selectOptions(matches)
	if len(opts) != maxOptions {
		t.Fatalf("options = %d, want %d", len(opts), maxOptions)
	}
	if n := utf8.RuneCountInString(opts[0].Label); n != maxLabelRunes {
		t.Errorf("label runes = %d, want %d", n, maxLabelRunes)
	}
	if opts[0].Value != "movie:1" {
		t.Errorf("value = %q", opts[0].Value)
	}
	if got := optionLabel(Match{Kind: kindTV, Title: "Dark", Year: "2017"}); got != "Dark (2017) · Serie" {
		t.Errorf("label = %q", got)
	}
}
