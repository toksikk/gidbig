package wttrin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func newTestModule() *Module {
	return &Module{
		detectLang:   func(_ *discordgo.Session, _ string) (string, error) { return "English", nil },
		generateFn:   func(_ context.Context, _, _ string) (string, error) { return "", nil },
		getWeatherFn: func(_ string) (wttrinResponse, error) { return wttrinResponse{}, nil },
		now:          time.Now,
		cache:        make(map[string]weatherCacheEntry),
		inflight:     make(map[string]*weatherCall),
	}
}

func TestBuildLLMWeatherOutro_ReturnsOnSuccess(t *testing.T) {
	m := newTestModule()
	m.detectLang = func(_ *discordgo.Session, _ string) (string, error) { return "German", nil }
	m.generateFn = func(_ context.Context, _, _ string) (string, error) {
		return "Das Wetter heute ist angenehm.", nil
	}

	outro := m.buildLLMWeatherOutro(nil, "ch1", "Berlin", "15°C sunny")

	if outro != "Das Wetter heute ist angenehm." {
		t.Errorf("unexpected outro: %q", outro)
	}
}

func TestBuildLLMWeatherOutro_EmptyOnLLMError(t *testing.T) {
	m := newTestModule()
	m.generateFn = func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("api error")
	}

	outro := m.buildLLMWeatherOutro(nil, "ch1", "London", "10°C rain")

	if outro != "" {
		t.Errorf("expected empty outro on LLM error, got %q", outro)
	}
}

func TestBuildLLMWeatherOutro_TrimsWhitespace(t *testing.T) {
	m := newTestModule()
	m.generateFn = func(_ context.Context, _, _ string) (string, error) {
		return "  Nice weather!  ", nil
	}

	outro := m.buildLLMWeatherOutro(nil, "ch1", "London", "data")

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

func TestBuildWeatherURL(t *testing.T) {
	locations := []string{"New York", "A#B", "what?where", "München", "north/east"}
	for _, location := range locations {
		t.Run(location, func(t *testing.T) {
			raw := buildWeatherURL(location)
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimPrefix(u.Path, "/"); got != location {
				t.Errorf("decoded path = %q, want %q (URL %q)", got, location, raw)
			}
			if got, want := u.EscapedPath(), "/"+url.PathEscape(location); got != want {
				t.Errorf("escaped path = %q, want %q", got, want)
			}
			if u.Query().Get("format") != "j1" || len(u.Query()) != 1 {
				t.Errorf("query = %v, want only format=j1", u.Query())
			}
		})
	}
}

type weatherDiscordRequest struct {
	method string
	path   string
	body   []byte
}

func weatherDiscordSession(t *testing.T) (*discordgo.Session, <-chan weatherDiscordRequest) {
	t.Helper()
	requests := make(chan weatherDiscordRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- weatherDiscordRequest{method: r.Method, path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	origAPI, origWebhooks := discordgo.EndpointAPI, discordgo.EndpointWebhooks
	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointWebhooks = server.URL + "/webhooks/"
	t.Cleanup(func() {
		discordgo.EndpointAPI = origAPI
		discordgo.EndpointWebhooks = origWebhooks
	})
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	return s, requests
}

func weatherInteraction(name string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "interaction", AppID: "app", Token: "token", Type: discordgo.InteractionApplicationCommand,
		ChannelID: "channel", Data: discordgo.ApplicationCommandInteractionData{Name: name, Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "location", Value: "Berlin"},
		}},
	}}
}

func awaitWeatherRequest(t *testing.T, requests <-chan weatherDiscordRequest) weatherDiscordRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Discord request")
		return weatherDiscordRequest{}
	}
}

func TestWeatherSlashHandlers_DeferAndEditSuccessAndError(t *testing.T) {
	tests := []struct {
		command string
		fail    bool
		want    string
	}{
		{"wttr", false, "## 📍"},
		{"wttrf", false, "### 📅"},
		{"wttr", true, "Failed to get weather for `Berlin`"},
		{"wttrf", true, "Failed to get weather for `Berlin`"},
	}
	for _, tt := range tests {
		name := tt.command + " success"
		if tt.fail {
			name = tt.command + " error"
		}
		t.Run(name, func(t *testing.T) {
			m := newTestModule()
			if tt.fail {
				m.getWeatherFn = func(string) (wttrinResponse, error) { return wttrinResponse{}, errors.New("offline") }
			} else {
				m.getWeatherFn = func(string) (wttrinResponse, error) { return minimalWeatherResponse(), nil }
			}
			s, requests := weatherDiscordSession(t)
			m.onInteractionCreate(s, weatherInteraction(tt.command))

			deferReq := awaitWeatherRequest(t, requests)
			var response discordgo.InteractionResponse
			if err := json.Unmarshal(deferReq.body, &response); err != nil {
				t.Fatal(err)
			}
			if response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
				t.Errorf("initial response type = %v, want deferred", response.Type)
			}
			editReq := awaitWeatherRequest(t, requests)
			var edit struct {
				Content *string `json:"content"`
			}
			if err := json.Unmarshal(editReq.body, &edit); err != nil {
				t.Fatal(err)
			}
			if edit.Content == nil || !strings.Contains(*edit.Content, tt.want) {
				t.Errorf("edited content = %v, want containing %q", edit.Content, tt.want)
			}
			if deferReq.method != http.MethodPost || editReq.method != http.MethodPatch {
				t.Errorf("request methods = %s, %s; want POST, PATCH", deferReq.method, editReq.method)
			}
		})
	}
}

func TestWeatherCommandsMetadata(t *testing.T) {
	commands := New().Commands()
	if len(commands) != 2 || commands[0].Name != "wttr" || commands[1].Name != "wttrf" {
		t.Fatalf("unexpected weather commands: %#v", commands)
	}
	for _, command := range commands {
		if len(command.Options) != 1 || !command.Options[0].Required || command.Options[0].MaxLength != 100 {
			t.Errorf("%s location metadata = %#v", command.Name, command.Options)
		}
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

func TestCheckForHighChances_ReturnsHighestValues(t *testing.T) {
	hours := []hourly{
		{Chanceoffog: "51", Chanceoffrost: "0", Chanceofhightemp: "0", Chanceofrain: "70", Chanceofsnow: "0", Chanceofthunder: "0", Chanceofwindy: "0"},
		{Chanceoffog: "80", Chanceoffrost: "0", Chanceofhightemp: "0", Chanceofrain: "60", Chanceofsnow: "0", Chanceofthunder: "0", Chanceofwindy: "0"},
	}

	if got, want := checkForHighChances(hours), "⚠️ 🌫️ 80% 🌧️ 70% "; got != want {
		t.Errorf("checkForHighChances() = %q, want %q", got, want)
	}
}

func TestCheckForHighChances_InvalidValueReturnsEmpty(t *testing.T) {
	hour := hourly{
		Chanceoffog: "invalid", Chanceoffrost: "0", Chanceofhightemp: "0", Chanceofrain: "70",
		Chanceofsnow: "0", Chanceofthunder: "0", Chanceofwindy: "0",
	}

	if got := checkForHighChances([]hourly{hour}); got != "" {
		t.Errorf("checkForHighChances() = %q, want empty result", got)
	}
}

func TestGetWeatherCached_CachesByNormalizedLocation(t *testing.T) {
	m := newTestModule()
	var calls int
	m.getWeatherFn = func(_ string) (wttrinResponse, error) {
		calls++
		return minimalWeatherResponse(), nil
	}

	if _, err := m.getWeatherCached("Berlin"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.getWeatherCached("berlin"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("weather fetch called %d times, want 1", calls)
	}
}

func TestGetWeatherCached_RefreshesExpiredEntry(t *testing.T) {
	m := newTestModule()
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	var calls int
	m.getWeatherFn = func(_ string) (wttrinResponse, error) {
		calls++
		return minimalWeatherResponse(), nil
	}

	if _, err := m.getWeatherCached("Berlin"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(weatherCacheTTL)
	if _, err := m.getWeatherCached("Berlin"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("weather fetch called %d times, want 2", calls)
	}
}

func TestGetWeatherCached_DoesNotCacheErrors(t *testing.T) {
	m := newTestModule()
	var calls int
	m.getWeatherFn = func(_ string) (wttrinResponse, error) {
		calls++
		return wttrinResponse{}, errors.New("unavailable")
	}

	for range 2 {
		if _, err := m.getWeatherCached("Berlin"); err == nil {
			t.Fatal("expected weather fetch error")
		}
	}
	if calls != 2 {
		t.Errorf("weather fetch called %d times, want 2", calls)
	}
}

func TestGetWeatherCached_CoalescesConcurrentRequests(t *testing.T) {
	m := newTestModule()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	m.getWeatherFn = func(_ string) (wttrinResponse, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return minimalWeatherResponse(), nil
	}

	const requests = 8
	var wg sync.WaitGroup
	wg.Add(requests)
	for range requests {
		go func() {
			defer wg.Done()
			if _, err := m.getWeatherCached("Berlin"); err != nil {
				t.Errorf("getWeatherCached() error = %v", err)
			}
		}()
	}
	<-started
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("weather fetch called %d times, want 1", got)
	}
}

func TestComposeWeather_StructuredBeforeLLMOutro(t *testing.T) {
	m := newTestModule()
	m.generateFn = func(_ context.Context, _, _ string) (string, error) { return "Lovely day ahead!", nil }
	m.getWeatherFn = func(_ string) (wttrinResponse, error) { return minimalWeatherResponse(), nil }

	msg, err := m.composeWeather(nil, "ch1", "Berlin", false)
	if err != nil {
		t.Fatal(err)
	}

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

func TestComposeWeather_ForecastStructuredBeforeLLMOutro(t *testing.T) {
	m := newTestModule()
	m.generateFn = func(_ context.Context, _, _ string) (string, error) { return "Pack an umbrella!", nil }
	m.getWeatherFn = func(_ string) (wttrinResponse, error) { return minimalWeatherResponse(), nil }

	msg, err := m.composeWeather(nil, "ch1", "Berlin", true)
	if err != nil {
		t.Fatal(err)
	}

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

func TestComposeWeather_LLMFailureReturnsOnlyStructured(t *testing.T) {
	m := newTestModule()
	m.generateFn = func(_ context.Context, _, _ string) (string, error) { return "", errors.New("llm down") }
	m.getWeatherFn = func(_ string) (wttrinResponse, error) { return minimalWeatherResponse(), nil }

	msg, err := m.composeWeather(nil, "ch1", "Berlin", false)
	if err != nil {
		t.Fatal(err)
	}

	if msg == "" {
		t.Fatal("expected structured weather, got empty string")
	}
	if strings.Contains(msg, "llm down") {
		t.Error("error message must not leak into output")
	}
}

func TestComposeWeather_WeatherFetchError(t *testing.T) {
	m := newTestModule()
	m.getWeatherFn = func(_ string) (wttrinResponse, error) { return wttrinResponse{}, errors.New("no network") }

	if _, err := m.composeWeather(nil, "ch1", "Berlin", false); err == nil {
		t.Fatal("expected error when weather fetch fails")
	}
}
