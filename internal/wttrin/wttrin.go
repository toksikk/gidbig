package wttrin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/llm"
)

const (
	baseURL   = "https://wttr.in/"
	apiSuffix = "?format=j1"
)

var weatherCodes = map[string]string{
	"113": "☀️",
	"116": "⛅",
	"119": "☁️",
	"122": "☁️",
	"143": "🌫️",
	"176": "🌦️",
	"179": "🌨️",
	"182": "🌨️",
	"185": "🌨️",
	"200": "⛈️",
	"227": "🌨️",
	"230": "❄️",
	"248": "🌫️",
	"260": "🌫️",
	"263": "🌦️",
	"266": "🌧️",
	"281": "🌨️",
	"284": "🌨️",
	"293": "🌧️",
	"296": "🌧️",
	"299": "🌧️",
	"302": "🌧️",
	"305": "🌧️",
	"308": "🌧️",
	"311": "🌨️",
	"314": "🌨️",
	"317": "🌨️",
	"320": "🌨️",
	"323": "🌨️",
	"326": "🌨️",
	"329": "❄️",
	"332": "❄️",
	"335": "❄️",
	"338": "❄️",
	"350": "🌨️",
	"353": "🌦️",
	"356": "🌧️",
	"359": "🌧️",
	"362": "🌨️",
	"365": "🌨️",
	"368": "🌨️",
	"371": "❄️",
	"374": "🌨️",
	"377": "🌨️",
	"386": "⛈️",
	"389": "⛈️",
	"392": "❄️⛈️",
	"395": "❄️",
}

var weatherDescriptions = map[string]string{
	"113": "Sunny",
	"116": "Partly Cloudy",
	"119": "Cloudy",
	"122": "Very Cloudy",
	"143": "Fog",
	"176": "Light Showers",
	"179": "Light Sleet Showers",
	"182": "Light Sleet",
	"185": "Light Sleet",
	"200": "Thundery Showers",
	"227": "Light Snow",
	"230": "Heavy Snow",
	"248": "Fog",
	"260": "Fog",
	"263": "Light Showers",
	"266": "Light Rain",
	"281": "Light Sleet",
	"284": "Light Sleet",
	"293": "Light Rain",
	"296": "Light Rain",
	"299": "Heavy Showers",
	"302": "Heavy Rain",
	"305": "Heavy Showers",
	"308": "Heavy Rain",
	"311": "Light Sleet",
	"314": "Light Sleet",
	"317": "Light Sleet",
	"320": "Light Snow",
	"323": "Light Snow Showers",
	"326": "Light Snow Showers",
	"329": "Heavy Snow",
	"332": "Heavy Snow",
	"335": "Heavy Snow Showers",
	"338": "Heavy Snow",
	"350": "Light Sleet",
	"353": "Light Showers",
	"356": "Heavy Showers",
	"359": "Heavy Rain",
	"362": "Light Sleet Showers",
	"365": "Light Sleet Showers",
	"368": "Light Snow Showers",
	"371": "Heavy Snow Showers",
	"374": "Light Sleet Showers",
	"377": "Light Sleet",
	"386": "Thundery Showers",
	"389": "Thundery Heavy Rain",
	"392": "Thundery Snow Showers",
	"395": "Heavy Snow Showers",
}

type hourly struct {
	DewPointC        string `json:"DewPointC"`
	DewPointF        string `json:"DewPointF"`
	FeelsLikeC       string `json:"FeelsLikeC"`
	FeelsLikeF       string `json:"FeelsLikeF"`
	HeatIndexC       string `json:"HeatIndexC"`
	HeatIndexF       string `json:"HeatIndexF"`
	WindChillC       string `json:"WindChillC"`
	WindChillF       string `json:"WindChillF"`
	WindGustKmph     string `json:"WindGustKmph"`
	WindGustMiles    string `json:"WindGustMiles"`
	Chanceoffog      string `json:"chanceoffog"`
	Chanceoffrost    string `json:"chanceoffrost"`
	Chanceofhightemp string `json:"chanceofhightemp"`
	Chanceofovercast string `json:"chanceofovercast"`
	Chanceofrain     string `json:"chanceofrain"`
	Chanceofremdry   string `json:"chanceofremdry"`
	Chanceofsnow     string `json:"chanceofsnow"`
	Chanceofsunshine string `json:"chanceofsunshine"`
	Chanceofthunder  string `json:"chanceofthunder"`
	Chanceofwindy    string `json:"chanceofwindy"`
	Cloudcover       string `json:"cloudcover"`
	Humidity         string `json:"humidity"`
	PrecipInches     string `json:"precipInches"`
	PrecipMM         string `json:"precipMM"`
	Pressure         string `json:"pressure"`
	PressureInches   string `json:"pressureInches"`
	TempC            string `json:"tempC"`
	TempF            string `json:"tempF"`
	Time             string `json:"time"`
	UvIndex          string `json:"uvIndex"`
	Visibility       string `json:"visibility"`
	VisibilityMiles  string `json:"visibilityMiles"`
	WeatherCode      string `json:"weatherCode"`
	WeatherDesc      []struct {
		Value string `json:"value"`
	} `json:"weatherDesc"`
	WeatherIconURL []struct {
		Value string `json:"value"`
	} `json:"weatherIconUrl"`
	Winddir16Point string `json:"winddir16Point"`
	WinddirDegree  string `json:"winddirDegree"`
	WindspeedKmph  string `json:"windspeedKmph"`
	WindspeedMiles string `json:"windspeedMiles"`
}

type wttrinResponse struct {
	CurrentCondition []struct {
		FeelsLikeC       string `json:"FeelsLikeC"`
		FeelsLikeF       string `json:"FeelsLikeF"`
		Cloudcover       string `json:"cloudcover"`
		Humidity         string `json:"humidity"`
		LocalObsDateTime string `json:"localObsDateTime"`
		ObservationTime  string `json:"observation_time"`
		PrecipInches     string `json:"precipInches"`
		PrecipMM         string `json:"precipMM"`
		Pressure         string `json:"pressure"`
		PressureInches   string `json:"pressureInches"`
		TempC            string `json:"temp_C"`
		TempF            string `json:"temp_F"`
		UvIndex          string `json:"uvIndex"`
		Visibility       string `json:"visibility"`
		VisibilityMiles  string `json:"visibilityMiles"`
		WeatherCode      string `json:"weatherCode"`
		WeatherDesc      []struct {
			Value string `json:"value"`
		} `json:"weatherDesc"`
		WeatherIconURL []struct {
			Value string `json:"value"`
		} `json:"weatherIconUrl"`
		Winddir16Point string `json:"winddir16Point"`
		WinddirDegree  string `json:"winddirDegree"`
		WindspeedKmph  string `json:"windspeedKmph"`
		WindspeedMiles string `json:"windspeedMiles"`
	} `json:"current_condition"`
	NearestArea []struct {
		AreaName []struct {
			Value string `json:"value"`
		} `json:"areaName"`
		Country []struct {
			Value string `json:"value"`
		} `json:"country"`
		Latitude   string `json:"latitude"`
		Longitude  string `json:"longitude"`
		Population string `json:"population"`
		Region     []struct {
			Value string `json:"value"`
		} `json:"region"`
		WeatherURL []struct {
			Value string `json:"value"`
		} `json:"weatherUrl"`
	} `json:"nearest_area"`
	Request []struct {
		Query string `json:"query"`
		Type  string `json:"type"`
	} `json:"request"`
	Weather []struct {
		Astronomy []struct {
			MoonIllumination string `json:"moon_illumination"`
			MoonPhase        string `json:"moon_phase"`
			Moonrise         string `json:"moonrise"`
			Moonset          string `json:"moonset"`
			Sunrise          string `json:"sunrise"`
			Sunset           string `json:"sunset"`
		} `json:"astronomy"`
		AvgtempC    string   `json:"avgtempC"`
		AvgtempF    string   `json:"avgtempF"`
		Date        string   `json:"date"`
		Hourly      []hourly `json:"hourly"`
		MaxtempC    string   `json:"maxtempC"`
		MaxtempF    string   `json:"maxtempF"`
		MintempC    string   `json:"mintempC"`
		MintempF    string   `json:"mintempF"`
		SunHour     string   `json:"sunHour"`
		TotalSnowCm string   `json:"totalSnow_cm"`
		UvIndex     string   `json:"uvIndex"`
	} `json:"weather"`
}

// Module implements bot.Module for the wttrin weather plugin.
type Module struct {
	session      *discordgo.Session
	detectLang   func(*discordgo.Session, string) (string, error)
	generateFn   func(context.Context, string, string) (string, error)
	getWeatherFn func(string) (wttrinResponse, error)
}

// New returns a new wttrin Module with default LLM functions.
func New() *Module {
	return &Module{
		detectLang:   llm.DetectChannelLanguage,
		generateFn:   llm.GenerateMessage,
		getWeatherFn: getWeather,
	}
}

func (m *Module) Name() string { return "wttrin" }

func (m *Module) Init(d bot.Deps) error {
	m.session = d.Session
	if d.LLM != nil {
		llmClient := d.LLM
		m.generateFn = func(ctx context.Context, system, user string) (string, error) {
			return llm.GenerateMessageWith(ctx, llmClient, system, user)
		}
	}
	slog.Info("wttrin: initialized")
	return nil
}

func (m *Module) Commands() []*discordgo.ApplicationCommand { return nil }

func (m *Module) Listeners() []bot.EventListener {
	return []bot.EventListener{m.onMessageCreate}
}

func (m *Module) Components() []bot.ComponentHandler { return nil }
func (m *Module) Background() []bot.BackgroundTask   { return nil }
func (m *Module) Shutdown() error                    { return nil }

func (m *Module) onMessageCreate(s *discordgo.Session, mc *discordgo.MessageCreate) {
	msg := strings.Replace(mc.ContentWithMentionsReplaced(), s.State.User.Username, "username", 1)
	parts := strings.Split(msg, " ")
	channel, err := s.State.Channel(mc.ChannelID)
	if channel == nil {
		slog.Error("Failed to grab channel", "MessageID", mc.ID, "ChannelID", mc.ChannelID, "Error", err)
		return
	}
	guild, err := s.State.Guild(channel.GuildID)
	if guild == nil {
		slog.Error("Failed to grab guild", "MessageID", mc.ID, "Channel", channel, "GuildID", channel.GuildID, "Error", err)
		return
	}
	switch strings.ToLower(parts[0]) {
	case "!wttr":
		m.sendMessage(s, mc, m.constructDiscordMessage(s, mc, parts, guild, false))
	case "!wttrf":
		m.sendMessage(s, mc, m.constructDiscordMessage(s, mc, parts, guild, true))
	}
}

func (m *Module) sendMessage(s *discordgo.Session, mc *discordgo.MessageCreate, message string) {
	// Empty string means the caller already handled the error (logged + notified user).
	if message == "" {
		return
	}
	if _, err := s.ChannelMessageSend(mc.ChannelID, message); err != nil {
		slog.Error("Failed to send message", "ChannelID", mc.ChannelID, "Error", err)
	}
}

func (m *Module) constructDiscordMessage(s *discordgo.Session, mc *discordgo.MessageCreate, parts []string, g *discordgo.Guild, forecast bool) string {
	if len(parts) <= 1 {
		return ""
	}
	location := strings.Join(parts[1:], "+")
	weatherResult, err := m.getWeatherFn(location)
	if err != nil {
		slog.Error("Failed to get weather", "MessageID", mc.ID, "Location", location, "Error", err)
		if _, err2 := s.ChannelMessageSend(mc.ChannelID, "Failed to get weather for "+location+": "+err.Error()); err2 != nil {
			slog.Error("Failed to send error message", "ChannelID", mc.ChannelID, "Error", err2)
		}
		return ""
	}
	var structured string
	if forecast {
		structured = buildForecastString(weatherResult)
	} else {
		structured = buildWeatherString(weatherResult)
	}
	outro := m.buildLLMWeatherOutro(s, mc, location, structured)
	if outro != "" {
		return structured + "\n" + outro
	}
	return structured
}

func (m *Module) buildLLMWeatherOutro(s *discordgo.Session, mc *discordgo.MessageCreate, location, weatherData string) string {
	lang, err := m.detectLang(s, mc.ChannelID)
	if err != nil {
		slog.Warn("wttrin: language detection failed", "error", err)
		lang = "English"
	}
	systemPrompt := "Discord bot. One sentence on current weather for the location — set mood, no raw numbers. " + llm.Personality + " Respond in " + lang + "."
	userPrompt := "Location: " + location + "\n" + weatherData
	outro, err := m.generateFn(context.Background(), systemPrompt, userPrompt)
	if err != nil {
		slog.Warn("wttrin: LLM outro generation failed", "error", err)
		return ""
	}
	return strings.TrimSpace(outro)
}

func getWindDirectionEmoji(winddirDegree int) (windDirectionEmoji string) {
	if (winddirDegree >= 337 && winddirDegree <= 360) || (winddirDegree >= 0 && winddirDegree <= 22) {
		windDirectionEmoji = "⬆️"
	} else if winddirDegree >= 22 && winddirDegree <= 67 {
		windDirectionEmoji = "↗️"
	} else if winddirDegree >= 67 && winddirDegree <= 112 {
		windDirectionEmoji = "➡️"
	} else if winddirDegree >= 112 && winddirDegree <= 157 {
		windDirectionEmoji = "↘️"
	} else if winddirDegree >= 157 && winddirDegree <= 202 {
		windDirectionEmoji = "⬇️"
	} else if winddirDegree >= 202 && winddirDegree <= 247 {
		windDirectionEmoji = "↙️"
	} else if winddirDegree >= 247 && winddirDegree <= 292 {
		windDirectionEmoji = "⬅️"
	} else if winddirDegree >= 292 && winddirDegree <= 337 {
		windDirectionEmoji = "↖️"
	}
	return
}

func getWeatherConditionEmoji(weatherCode string) (weatherConditionEmoji string) {
	weatherConditionEmoji = "🌈"
	for code := range weatherCodes {
		if weatherCode == code {
			weatherConditionEmoji = weatherCodes[code]
			break
		}
	}
	if weatherConditionEmoji == "🌈" {
		slog.Warn("Unknown weather code", "Code", weatherCode)
	}
	return
}

func addLocationToString(weatherResult wttrinResponse) (r string) {
	var region string
	if weatherResult.NearestArea[0].Region[0].Value != "" {
		region = "(" + weatherResult.NearestArea[0].Region[0].Value + ")"
	}
	r += "## 📍 [" + weatherResult.NearestArea[0].AreaName[0].Value + ", " + weatherResult.NearestArea[0].Country[0].Value + " " + region + "](<https://www.google.com/maps/place/" + weatherResult.NearestArea[0].Latitude + "," + weatherResult.NearestArea[0].Longitude + ">)\n"
	return
}

func buildWeatherString(weatherResult wttrinResponse) (result string) {
	weatherConditionEmoji := getWeatherConditionEmoji(weatherResult.CurrentCondition[0].WeatherCode)
	windDirDegree, err := strconv.Atoi(weatherResult.CurrentCondition[0].WinddirDegree)
	if err != nil {
		slog.Error("Failed to convert wind direction to integer", "weatherResult.CurrentCondition[0].WinddirDegree", weatherResult.CurrentCondition[0].WinddirDegree, "Error", err)
		return
	}
	windDirectionEmoji := getWindDirectionEmoji(windDirDegree)

	result += addLocationToString(weatherResult)

	result += "```\n" +
		"🌡️ " + weatherResult.CurrentCondition[0].TempC + "°C (feels like " + weatherResult.CurrentCondition[0].FeelsLikeC + "°C)\n" +
		"💧 " + weatherResult.CurrentCondition[0].Humidity + "% humidity\n" +
		"🌬️ " + windDirectionEmoji + " " + weatherResult.CurrentCondition[0].WindspeedKmph + "km/h\n" +
		weatherConditionEmoji + " " + weatherResult.CurrentCondition[0].WeatherDesc[0].Value + "\n" + checkForHighChances(weatherResult.Weather[0].Hourly) + "\n```"
	return
}

func mostOccurringWeatherCodeForDay(hours []hourly) string {
	if len(hours) == 0 {
		return ""
	}
	weatherCodeCounts := make(map[string]int)
	for _, hour := range hours {
		weatherCodeCounts[hour.WeatherCode]++
	}
	maxCount := 0
	mostOccurringCode := ""
	for code, count := range weatherCodeCounts {
		if count > maxCount {
			mostOccurringCode = code
			maxCount = count
		}
	}
	return mostOccurringCode
}

func checkForHighChances(hourly []hourly) (highChances string) {
	highestChanceOfFog := 0
	highestChanceOfFrost := 0
	highestChanceOfHighTemp := 0
	highestChanceOfRain := 0
	highestChanceOfSnow := 0
	highestChanceOfThunder := 0
	highestChanceOfWindy := 0

	for _, hour := range hourly {
		chanceoffog, err := strconv.Atoi(hour.Chanceoffog)
		if err != nil {
			slog.Error("Failed to convert chanceoffog to integer", "hour.Chanceoffog", hour.Chanceoffog, "Error", err)
			return
		}
		chanceoffrost, err := strconv.Atoi(hour.Chanceoffrost)
		if err != nil {
			slog.Error("Failed to convert chanceoffrost to integer", "hour.Chanceoffrost", hour.Chanceoffrost, "Error", err)
			return
		}
		chanceofhightemp, err := strconv.Atoi(hour.Chanceofhightemp)
		if err != nil {
			slog.Error("Failed to convert chanceofhightemp to integer", "hour.Chanceofhightemp", hour.Chanceofhightemp, "Error", err)
			return
		}
		chanceofrain, err := strconv.Atoi(hour.Chanceofrain)
		if err != nil {
			slog.Error("Failed to convert chanceofrain to integer", "hour.Chanceofrain", hour.Chanceofrain, "Error", err)
			return
		}
		chanceofsnow, err := strconv.Atoi(hour.Chanceofsnow)
		if err != nil {
			slog.Error("Failed to convert chanceofsnow to integer", "hour.Chanceofsnow", hour.Chanceofsnow, "Error", err)
			return
		}
		chanceofthunder, err := strconv.Atoi(hour.Chanceofthunder)
		if err != nil {
			slog.Error("Failed to convert chanceofthunder to integer", "hour.Chanceofthunder", hour.Chanceofthunder, "Error", err)
			return
		}
		chanceofwindy, err := strconv.Atoi(hour.Chanceofwindy)
		if err != nil {
			slog.Error("Failed to convert chanceofwindy to integer", "hour.Chanceofwindy", hour.Chanceofwindy, "Error", err)
			return
		}
		if chanceoffog > 50 && chanceoffog > highestChanceOfFog {
			highestChanceOfFog = chanceoffog
		}
		if chanceoffrost > 50 && chanceoffrost > highestChanceOfFrost {
			highestChanceOfFrost = chanceoffrost
		}
		if chanceofhightemp > 50 && chanceofhightemp > highestChanceOfHighTemp {
			highestChanceOfHighTemp = chanceofhightemp
		}
		if chanceofrain > 50 && chanceofrain > highestChanceOfRain {
			highestChanceOfRain = chanceofrain
		}
		if chanceofsnow > 50 && chanceofsnow > highestChanceOfSnow {
			highestChanceOfSnow = chanceofsnow
		}
		if chanceofthunder > 50 && chanceofthunder > highestChanceOfThunder {
			highestChanceOfThunder = chanceofthunder
		}
		if chanceofwindy > 50 && chanceofwindy > highestChanceOfWindy {
			highestChanceOfWindy = chanceofwindy
		}
	}
	if highestChanceOfFog > 0 {
		highChances += "🌫️ " + strconv.Itoa(highestChanceOfFog) + "% "
	}
	if highestChanceOfFrost > 0 {
		highChances += "🥶 " + strconv.Itoa(highestChanceOfFrost) + "% "
	}
	if highestChanceOfHighTemp > 0 {
		highChances += "🥵 " + strconv.Itoa(highestChanceOfHighTemp) + "% "
	}
	if highestChanceOfRain > 0 {
		highChances += "🌧️ " + strconv.Itoa(highestChanceOfRain) + "% "
	}
	if highestChanceOfSnow > 0 {
		highChances += "❄️ " + strconv.Itoa(highestChanceOfSnow) + "% "
	}
	if highestChanceOfThunder > 0 {
		highChances += "⛈️ " + strconv.Itoa(highestChanceOfThunder) + "% "
	}
	if highestChanceOfWindy > 0 {
		highChances += "💨 " + strconv.Itoa(highestChanceOfWindy) + "% "
	}
	if highChances != "" {
		highChances = "⚠️ " + highChances
	}
	return
}

func buildForecastString(weatherResult wttrinResponse) (result string) {
	result += addLocationToString(weatherResult)
	for i, day := range weatherResult.Weather {
		if i > 0 {
			result += "```\n"
		}

		weatherCode := mostOccurringWeatherCodeForDay(day.Hourly)
		weatherConditionEmoji := getWeatherConditionEmoji(weatherCode)

		avgWindDirDegree := 0
		for _, hour := range day.Hourly {
			windDirDegree, err := strconv.Atoi(hour.WinddirDegree)
			if err != nil {
				slog.Error("Failed to convert wind direction to integer", "hour.WinddirDegree", hour.WinddirDegree, "Error", err)
				return
			}
			avgWindDirDegree += windDirDegree
		}
		avgWindDirDegree /= len(day.Hourly)

		windDirectionEmoji := getWindDirectionEmoji(avgWindDirDegree)

		avgWindspeedKmph := 0
		for _, hour := range day.Hourly {
			windspeedKmph, err := strconv.Atoi(hour.WindspeedKmph)
			if err != nil {
				slog.Error("Failed to convert wind speed to integer", "hour.WindspeedKmph", hour.WindspeedKmph, "Error", err)
				return
			}
			avgWindspeedKmph += windspeedKmph
		}
		avgWindspeedKmph /= len(day.Hourly)

		result += "### 📅 " + day.Date + "\n```\n" +
			"🌡️ " + day.MaxtempC + "°C / " + day.MintempC + "°C\n" +
			"🌬️ " + windDirectionEmoji + " " + strconv.Itoa(avgWindspeedKmph) + "km/h\n" +
			weatherConditionEmoji + " " + weatherDescriptions[weatherCode] + "\n"

		totalSnow, err := strconv.ParseFloat(day.TotalSnowCm, 32)
		if err != nil {
			slog.Warn("Failed to parse total snow", "TotalSnowCm", day.TotalSnowCm, "Error", err)
		} else {
			if totalSnow > 0.0 {
				result += "❄️ " + day.TotalSnowCm + "cm"
			}
		}

		totalRain := 0.0
		for _, hour := range day.Hourly {
			rain, err := strconv.ParseFloat(hour.PrecipMM, 32)
			if err != nil {
				slog.Warn("Failed to parse total rain", "PrecipMM", hour.PrecipMM, "Error", err)
			}
			totalRain += rain
		}

		if totalRain > 0.0 {
			averageRain := totalRain / float64(len(day.Hourly))
			if averageRain > 0.0 {
				if totalSnow > 0.0 {
					result += " / "
				}
				result += fmt.Sprintf("🌧️ %.2fmm\n", averageRain)
			} else {
				result += "\n"
			}
		}

		highChances := checkForHighChances(day.Hourly)
		if highChances != "" {
			result += highChances + "\n"
		}
	}
	result += "```"
	return
}

func getWeather(location string) (weatherResult wttrinResponse, err error) {
	nocache := "&nonce=" + strconv.Itoa(rand.Intn(32768))
	queryURL := baseURL + location + apiSuffix + nocache
	slog.Info("Querying wttr.in", "URL", queryURL)
	return httpGet(queryURL)
}

func httpGet(url string) (weatherResult wttrinResponse, err error) {
	var resp *http.Response
	var httpClient = &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err = httpClient.Get(url)
	if err != nil {
		slog.Error("Failed to get weather", "URL", url, "Error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		slog.Info("Could not find requested location", "URL", url, "StatusCode", resp.StatusCode)
		err = fmt.Errorf("%s", resp.Status)
		return
	}

	var body []byte
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("Failed to read response body", "URL", url, "Error", err)
		return
	}

	err = json.Unmarshal(body, &weatherResult)
	slog.Debug("Got weather", "URL", url, "Response", weatherResult)
	return
}
