package openmeteo

import "fmt"

type wmoInfo struct {
	desc  string
	emoji string
}

// wmoCodes maps WMO weather interpretation codes to a short description.
var wmoCodes = map[int]wmoInfo{
	0:  {"clear", "☀️"},
	1:  {"mainly clear", "🌤️"},
	2:  {"partly cloudy", "⛅"},
	3:  {"overcast", "☁️"},
	45: {"fog", "🌫️"},
	48: {"rime fog", "🌫️"},
	51: {"light drizzle", "🌦️"},
	53: {"drizzle", "🌦️"},
	55: {"dense drizzle", "🌧️"},
	56: {"light freezing drizzle", "🌧️"},
	57: {"freezing drizzle", "🌧️"},
	61: {"light rain", "🌦️"},
	63: {"rain", "🌧️"},
	65: {"heavy rain", "🌧️"},
	66: {"light freezing rain", "🌧️"},
	67: {"freezing rain", "🌧️"},
	71: {"light snow", "🌨️"},
	73: {"snow", "🌨️"},
	75: {"heavy snow", "❄️"},
	77: {"snow grains", "🌨️"},
	80: {"light showers", "🌦️"},
	81: {"showers", "🌧️"},
	82: {"violent showers", "⛈️"},
	85: {"light snow showers", "🌨️"},
	86: {"snow showers", "❄️"},
	95: {"thunderstorm", "⛈️"},
	96: {"thunderstorm with hail", "⛈️"},
	99: {"thunderstorm with heavy hail", "⛈️"},
}

func wmo(code int) wmoInfo {
	if w, ok := wmoCodes[code]; ok {
		return w
	}
	return wmoInfo{desc: fmt.Sprintf("unknown (%d)", code), emoji: "❔"}
}
