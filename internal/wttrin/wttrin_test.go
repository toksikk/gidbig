package wttrin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func newTestModule() *Module {
	return &Module{
		detectLang:   func(_ *discordgo.Session, _ string) (string, error) { return "English", nil },
		generateFn:   func(_ context.Context, _, _ string) (string, error) { return "", nil },
		getWeatherFn: func(_ string) (wttrinResponse, error) { return wttrinResponse{}, nil },
	}
}

func TestBuildLLMWeatherOutro_ReturnsOnSuccess(t *testing.T) {
	m := newTestModule()
	m.detectLang = func(_ *discordgo.Session, _ string) (string, error) { return "German", nil }
	m.generateFn = func(_ context.Context, _, _ string) (string, error) {
		return "Das Wetter heute ist angenehm.", nil
	}

	outro := m.buildLLMWeatherOutro(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, "Berlin", "15°C sunny")

	if outro != "Das Wetter heute ist angenehm." {
		t.Errorf("unexpected outro: %q", outro)
	}
}

func TestBuildLLMWeatherOutro_EmptyOnLLMError(t *testing.T) {
	m := newTestModule()
	m.generateFn = func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("api error")
	}

	outro := m.buildLLMWeatherOutro(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, "London", "10°C rain")

	if outro != "" {
		t.Errorf("expected empty outro on LLM error, got %q", outro)
	}
}

func TestBuildLLMWeatherOutro_TrimsWhitespace(t *testing.T) {
	m := newTestModule()
	m.generateFn = func(_ context.Context, _, _ string) (string, error) {
		return "  Nice weather!  ", nil
	}

	outro := m.buildLLMWeatherOutro(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, "London", "data")

	if outro != "Nice weather!" {
		t.Errorf("expected trimmed outro, got %q", outro)
	}
}

func TestDefaultsWiredCorrectly(t *testing.T) {
	m := New()
	if m.generateFn == nil {
		t.Error("generateFn must not be nil")
	}
	if m.detectLang == nil {
		t.Error("detectLang must not be nil")
	}
	if m.getWeatherFn == nil {
		t.Error("getWeatherFn must not be nil")
	}
}

func minimalWeatherResponse() wttrinResponse {
	return wttrinResponse{
		CurrentCondition: []struct {
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
		}{
			{
				TempC: "15", FeelsLikeC: "13", Humidity: "60",
				WindspeedKmph: "20", WinddirDegree: "90",
				WeatherCode: "113",
				WeatherDesc: []struct {
					Value string `json:"value"`
				}{{Value: "Sunny"}},
			},
		},
		NearestArea: []struct {
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
		}{
			{
				AreaName: []struct {
					Value string `json:"value"`
				}{{Value: "Berlin"}},
				Country: []struct {
					Value string `json:"value"`
				}{{Value: "Germany"}},
				Region: []struct {
					Value string `json:"value"`
				}{{Value: ""}},
				Latitude: "52.5", Longitude: "13.4",
			},
		},
		Weather: []struct {
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
		}{
			{
				Date: "2026-05-04", MaxtempC: "18", MintempC: "10",
				AvgtempC: "14", TotalSnowCm: "0",
				Hourly: []hourly{
					{
						WinddirDegree: "90", WindspeedKmph: "20",
						WeatherCode: "113",
						WeatherDesc: []struct {
							Value string `json:"value"`
						}{{Value: "Sunny"}},
						Chanceoffog: "0", Chanceoffrost: "0", Chanceofhightemp: "0",
						Chanceofrain: "0", Chanceofsnow: "0", Chanceofthunder: "0",
						Chanceofwindy: "0", PrecipMM: "0",
					},
				},
			},
		},
	}
}

func TestBuildForecastString_UsesEachDaysMostOccurringWeatherCode(t *testing.T) {
	weatherResult := minimalWeatherResponse()
	weatherResult.Weather[0].Hourly = append(weatherResult.Weather[0].Hourly,
		weatherResult.Weather[0].Hourly[0],
		weatherResult.Weather[0].Hourly[0],
	)

	secondDay := weatherResult.Weather[0]
	secondDay.Date = "2026-05-05"
	secondDay.MaxtempC = "12"
	secondDay.MintempC = "7"
	secondDay.Hourly = nil
	for range 4 {
		hour := weatherResult.Weather[0].Hourly[0]
		hour.WeatherCode = "308"
		secondDay.Hourly = append(secondDay.Hourly, hour)
	}
	weatherResult.Weather = append(weatherResult.Weather, secondDay)

	forecast := buildForecastString(weatherResult)

	if !strings.Contains(forecast, "### 📅 2026-05-04\n```\n🌡️ 18°C / 10°C\n🌬️ ➡️ 20km/h\n☀️ Sunny") {
		t.Errorf("first day should use its own dominant weather code, got:\n%s", forecast)
	}
	if !strings.Contains(forecast, "### 📅 2026-05-05\n```\n🌡️ 12°C / 7°C\n🌬️ ➡️ 20km/h\n🌧️ Heavy Rain") {
		t.Errorf("second day should use its own dominant weather code, got:\n%s", forecast)
	}
}

func TestMostOccurringWeatherCodeForDay_EmptyInput(t *testing.T) {
	if got := mostOccurringWeatherCodeForDay(nil); got != "" {
		t.Errorf("expected empty string for nil input, got %q", got)
	}
	if got := mostOccurringWeatherCodeForDay([]hourly{}); got != "" {
		t.Errorf("expected empty string for empty slice, got %q", got)
	}
}

func TestMostOccurringWeatherCodeForDay_ReturnsDominantCode(t *testing.T) {
	hours := []hourly{
		{WeatherCode: "113"},
		{WeatherCode: "113"},
		{WeatherCode: "308"},
	}
	if got := mostOccurringWeatherCodeForDay(hours); got != "113" {
		t.Errorf("expected %q, got %q", "113", got)
	}
}

func TestConstructDiscordMessage_StructuredBeforeLLMOutro(t *testing.T) {
	m := newTestModule()
	m.generateFn = func(_ context.Context, _, _ string) (string, error) { return "Lovely day ahead!", nil }
	m.getWeatherFn = func(_ string) (wttrinResponse, error) { return minimalWeatherResponse(), nil }

	msg := m.constructDiscordMessage(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, []string{"!wttr", "Berlin"}, &discordgo.Guild{}, false)

	outroIdx := strings.Index(msg, "Lovely day ahead!")
	if outroIdx == -1 {
		t.Fatal("LLM outro missing from message")
	}
	structuredIdx := strings.Index(msg, "## 📍")
	if structuredIdx == -1 {
		t.Fatal("structured weather missing from message")
	}
	if structuredIdx > outroIdx {
		t.Errorf("structured weather must appear before LLM outro: structuredIdx=%d outroIdx=%d", structuredIdx, outroIdx)
	}
}

func TestConstructDiscordMessage_ForecastStructuredBeforeLLMOutro(t *testing.T) {
	m := newTestModule()
	m.generateFn = func(_ context.Context, _, _ string) (string, error) { return "Pack an umbrella!", nil }
	m.getWeatherFn = func(_ string) (wttrinResponse, error) { return minimalWeatherResponse(), nil }

	msg := m.constructDiscordMessage(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, []string{"!wttrf", "Berlin"}, &discordgo.Guild{}, true)

	outroIdx := strings.Index(msg, "Pack an umbrella!")
	if outroIdx == -1 {
		t.Fatal("LLM outro missing from forecast message")
	}
	structuredIdx := strings.Index(msg, "### 📅")
	if structuredIdx == -1 {
		t.Fatal("structured forecast missing from message")
	}
	if structuredIdx > outroIdx {
		t.Errorf("structured forecast must appear before LLM outro: structuredIdx=%d outroIdx=%d", structuredIdx, outroIdx)
	}
}

func TestConstructDiscordMessage_LLMFailureReturnsOnlyStructured(t *testing.T) {
	m := newTestModule()
	m.generateFn = func(_ context.Context, _, _ string) (string, error) { return "", errors.New("llm down") }
	m.getWeatherFn = func(_ string) (wttrinResponse, error) { return minimalWeatherResponse(), nil }

	msg := m.constructDiscordMessage(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, []string{"!wttr", "Berlin"}, &discordgo.Guild{}, false)

	if msg == "" {
		t.Fatal("expected structured weather, got empty string")
	}
	if strings.Contains(msg, "llm down") {
		t.Error("error message must not leak into output")
	}
}
