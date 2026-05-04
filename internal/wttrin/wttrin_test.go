package wttrin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/llm"
)

func stubLLM(t *testing.T, reply string, err error) {
	t.Helper()
	prev := generateLLMIntro
	t.Cleanup(func() { generateLLMIntro = prev })
	generateLLMIntro = func(_ context.Context, _, _ string) (string, error) {
		return reply, err
	}
}

func stubDetectLanguage(t *testing.T, lang string) {
	t.Helper()
	prev := detectLanguage
	t.Cleanup(func() { detectLanguage = prev })
	detectLanguage = func(_ *discordgo.Session, _ string) (string, error) {
		return lang, nil
	}
}

func stubGetWeather(t *testing.T, result wttrinResponse, err error) {
	t.Helper()
	prev := getWeatherFn
	t.Cleanup(func() { getWeatherFn = prev })
	getWeatherFn = func(_ string) (wttrinResponse, error) {
		return result, err
	}
}

func TestBuildLLMWeatherOutro_ReturnsOnSuccess(t *testing.T) {
	stubDetectLanguage(t, "German")
	stubLLM(t, "Das Wetter heute ist angenehm.", nil)

	outro := buildLLMWeatherOutro(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, "Berlin", "15°C sunny")

	if outro != "Das Wetter heute ist angenehm." {
		t.Errorf("unexpected outro: %q", outro)
	}
}

func TestBuildLLMWeatherOutro_EmptyOnLLMError(t *testing.T) {
	stubDetectLanguage(t, "English")
	stubLLM(t, "", errors.New("api error"))

	outro := buildLLMWeatherOutro(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, "London", "10°C rain")

	if outro != "" {
		t.Errorf("expected empty outro on LLM error, got %q", outro)
	}
}

func TestBuildLLMWeatherOutro_TrimsWhitespace(t *testing.T) {
	stubDetectLanguage(t, "English")
	stubLLM(t, "  Nice weather!  ", nil)

	outro := buildLLMWeatherOutro(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, "London", "data")

	if outro != "Nice weather!" {
		t.Errorf("expected trimmed outro, got %q", outro)
	}
}

// Verify the package-level vars are wired to the llm package.
func TestDefaultsWiredToLLMPackage(t *testing.T) {
	if generateLLMIntro == nil {
		t.Error("generateLLMIntro must not be nil")
	}
	_ = llm.GenerateMessage // ensure llm package is referenced
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

func TestConstructDiscordMessage_StructuredBeforeLLMOutro(t *testing.T) {
	stubDetectLanguage(t, "English")
	stubLLM(t, "Lovely day ahead!", nil)
	stubGetWeather(t, minimalWeatherResponse(), nil)

	msg := constructDiscordMessage(nil, &discordgo.MessageCreate{
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
	stubDetectLanguage(t, "English")
	stubLLM(t, "Pack an umbrella!", nil)
	stubGetWeather(t, minimalWeatherResponse(), nil)

	msg := constructDiscordMessage(nil, &discordgo.MessageCreate{
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
	stubDetectLanguage(t, "English")
	stubLLM(t, "", errors.New("llm down"))
	stubGetWeather(t, minimalWeatherResponse(), nil)

	msg := constructDiscordMessage(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, []string{"!wttr", "Berlin"}, &discordgo.Guild{}, false)

	if msg == "" {
		t.Fatal("expected structured weather, got empty string")
	}
	if strings.Contains(msg, "llm down") {
		t.Error("error message must not leak into output")
	}
}
