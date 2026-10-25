package tv

import (
	"bytes"
	"compress/gzip"
	"os"
	"strings"
	"testing"
	"time"
)

// berlin is the zone the fixtures and the default config use.
var berlin = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		panic(err)
	}
	return loc
}()

// fixtureNow is 2026-10-24 20:30 in Berlin (CEST), the night before the DST switch.
var fixtureNow = time.Date(2026, 10, 24, 20, 30, 0, 0, berlin)

var fixtureChannels = []string{"ZDF.de", "DasErste.de"}

func fixtureXML(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/epg_small.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return strings.Replace(string(b), "DESC_PLACEHOLDER", strings.Repeat("ä", 300), 1)
}

func fixtureGzip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(fixtureXML(t))); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func fixtureIndex(t *testing.T) *index {
	t.Helper()
	ix, err := parse(strings.NewReader(fixtureXML(t)), fixtureChannels, fixtureNow, berlin)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return ix
}

func titles(ps []programme) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Title
	}
	return out
}

func TestParse_FiltersAndCleans(t *testing.T) {
	ix := fixtureIndex(t)
	if len(ix.channels) != 2 || ix.channels[0].Name != "ZDF" || ix.channels[1].Name != "Das Erste" {
		t.Fatalf("channels = %+v", ix.channels)
	}
	zdf := ix.progs[0]
	got := strings.Join(titles(zdf), "|")
	// Rows before yesterday, "No Data" and SENDEPAUSE dropped; allowlist excludes Other.de.
	if got != "Morgenmagazin|heute|Der Alte|heute journal|Nachtfilm" {
		t.Fatalf("ZDF titles = %q", got)
	}
	alte := zdf[2]
	if alte.Sub != "Tod in München" || strings.Join(alte.Categories, ",") != "Krimi,Thriller" || alte.Desc != "Ein Fall & mehr." {
		t.Errorf("cleaned = %+v", alte)
	}
	// Missing stop is filled from the next start, the dropped SENDEPAUSE at 00:30.
	if want := time.Date(2026, 10, 25, 0, 30, 0, 0, time.FixedZone("", 7200)).Unix(); zdf[3].Stop != want {
		t.Errorf("filled stop = %d, want %d", zdf[3].Stop, want)
	}
	ard := ix.progs[1]
	// The +16 day row is outside the window.
	if len(ard) != 2 {
		t.Fatalf("ARD rows = %d, want 2", len(ard))
	}
	// No zone means UTC: 20:15 UTC.
	if want := time.Date(2026, 10, 24, 20, 15, 0, 0, time.UTC).Unix(); ard[0].Start != want {
		t.Errorf("zoneless start = %d, want %d", ard[0].Start, want)
	}
	if n := len([]rune(ard[0].Desc)); n != maxDescRunes || !strings.HasSuffix(ard[0].Desc, "…") {
		t.Errorf("desc runes = %d, suffix ok = %v", n, strings.HasSuffix(ard[0].Desc, "…"))
	}
}

func TestParse_Malformed(t *testing.T) {
	if _, err := parse(strings.NewReader(`<tv><programme channel="ZDF.de"><title>x</tv>`), fixtureChannels, fixtureNow, berlin); err == nil {
		t.Fatal("malformed XML parsed without error")
	}
}

func TestParseXMLTVTime(t *testing.T) {
	cases := map[string]time.Time{
		"20261024201500 +0200": time.Date(2026, 10, 24, 18, 15, 0, 0, time.UTC),
		"20261124201500 +0100": time.Date(2026, 11, 24, 19, 15, 0, 0, time.UTC),
		"20261024201500":       time.Date(2026, 10, 24, 20, 15, 0, 0, time.UTC),
	}
	for in, want := range cases {
		got, ok := parseXMLTVTime(in)
		if !ok || !got.Equal(want) {
			t.Errorf("parseXMLTVTime(%q) = %v %v, want %v", in, got, ok, want)
		}
	}
	if _, ok := parseXMLTVTime("garbage"); ok {
		t.Error("garbage parsed")
	}
}
