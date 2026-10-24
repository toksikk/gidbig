package tv

import (
	"strings"
	"unicode"
)

// defaultChannels is the built-in XMLTV channel ID allowlist, used when
// tv.channels is empty. Every ID was checked for programme data in the
// free-epg.de feed on 2026-10-24.
var defaultChannels = []string{
	// Public broadcasters.
	"DasErste.de", "ZDF.de", "zdf_neo.de", "ZDFinfo.de", "3sat.de", "ARTE.de",
	"phoenix.de", "tagesschau24.de", "ONE.de", "KiKA.de", "WDR.de", "NDR.de", "BR.de",
	// Private broadcasters.
	"RTL.de", "RTLZWEI.de", "SAT1.de", "ProSieben.de", "ProSiebenMAXX.de", "VOX.de",
	"KabelEins.de", "NITRO.de", "sixx.de", "SuperRTL.de", "DMAX.de", "Tele5.de",
	"n-tv.de", "WELT.de",
}

// builtinAliases adds common spoken names for channel IDs. The ID without its
// ".de" suffix and the feed's display name are always aliases as well.
var builtinAliases = map[string][]string{
	"DasErste.de":      {"ARD", "Das Erste", "Erste"},
	"zdf_neo.de":       {"ZDFneo", "ZDF neo"},
	"RTLZWEI.de":       {"RTL2", "RTL 2", "RTL II"},
	"SAT1.de":          {"Sat.1", "Sat 1"},
	"ProSieben.de":     {"Pro7", "Pro 7"},
	"ProSiebenMAXX.de": {"Pro7 Maxx", "Pro7MAXX"},
	"KabelEins.de":     {"Kabel 1", "Kabel1"},
	"SuperRTL.de":      {"Super RTL", "Toggo"},
	"n-tv.de":          {"ntv"},
	"WELT.de":          {"Welt", "N24"},
	"tagesschau24.de":  {"Tagesschau"},
	"KiKA.de":          {"Kinderkanal"},
	"BR.de":            {"Bayerisches Fernsehen", "BR Fernsehen"},
}

// builtinNames overrides feed display names that read poorly.
var builtinNames = map[string]string{"zdf_neo.de": "ZDFneo"}

// umlautFolder maps German special letters to their two-letter spelling so
// "München" and "muenchen" compare equal.
var umlautFolder = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")

// foldKey normalises a channel name for alias lookup: lower case, umlauts
// spelled out, and everything except letters and digits removed.
func foldKey(s string) string {
	s = umlautFolder.Replace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// foldText normalises free text for title search: lower case with umlauts
// spelled out and whitespace collapsed.
func foldText(s string) string {
	return strings.Join(strings.Fields(umlautFolder.Replace(strings.ToLower(s))), " ")
}

// channelLabel derives a readable channel name from the feed display name,
// which looks like "DE - Das Erste", falling back to the ID without ".de".
func channelLabel(id, display string) string {
	if name, ok := builtinNames[id]; ok {
		return name
	}
	display = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(display), "DE - "))
	if display != "" && display != id {
		return display
	}
	return strings.TrimSuffix(id, ".de")
}
