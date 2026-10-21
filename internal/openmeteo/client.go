package openmeteo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultForecastURL = "https://api.open-meteo.com/v1/forecast"
	defaultGeocodeURL  = "https://geocoding-api.open-meteo.com/v1/search"
	maxBodyBytes       = 1 << 20
	httpTimeout        = 15 * time.Second

	dailyVars   = "weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,snowfall_sum,precipitation_probability_max,sunshine_duration,wind_speed_10m_max,wind_gusts_10m_max,wind_direction_10m_dominant,uv_index_max,sunrise,sunset"
	hourlyVars  = "temperature_2m,precipitation,precipitation_probability,weather_code,cloud_cover,wind_speed_10m"
	currentVars = "temperature_2m,apparent_temperature,relative_humidity_2m,weather_code,wind_speed_10m,wind_direction_10m,precipitation,cloud_cover"
)

// errBodyTooLarge is returned when a response exceeds maxBodyBytes.
var errBodyTooLarge = errors.New("response body too large")

// place is a resolved location.
type place struct {
	Name        string  `json:"name"`
	Admin1      string  `json:"admin1"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

type geocodeResponse struct {
	Results []place `json:"results"`
}

// forecastResponse is the subset of the Open-Meteo forecast response used here.
type forecastResponse struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
	Current   struct {
		Temperature   float64 `json:"temperature_2m"`
		Apparent      float64 `json:"apparent_temperature"`
		Humidity      float64 `json:"relative_humidity_2m"`
		WeatherCode   int     `json:"weather_code"`
		WindSpeed     float64 `json:"wind_speed_10m"`
		WindDirection float64 `json:"wind_direction_10m"`
		Precipitation float64 `json:"precipitation"`
		CloudCover    float64 `json:"cloud_cover"`
	} `json:"current"`
	Daily struct {
		Time          []string  `json:"time"`
		WeatherCode   []int     `json:"weather_code"`
		TempMax       []float64 `json:"temperature_2m_max"`
		TempMin       []float64 `json:"temperature_2m_min"`
		PrecipSum     []float64 `json:"precipitation_sum"`
		SnowSum       []float64 `json:"snowfall_sum"`
		PrecipProbMax []float64 `json:"precipitation_probability_max"`
		Sunshine      []float64 `json:"sunshine_duration"`
		WindMax       []float64 `json:"wind_speed_10m_max"`
		GustsMax      []float64 `json:"wind_gusts_10m_max"`
		WindDir       []float64 `json:"wind_direction_10m_dominant"`
		UVMax         []float64 `json:"uv_index_max"`
		Sunrise       []string  `json:"sunrise"`
		Sunset        []string  `json:"sunset"`
	} `json:"daily"`
	Hourly struct {
		Time          []string  `json:"time"`
		Temperature   []float64 `json:"temperature_2m"`
		Precipitation []float64 `json:"precipitation"`
		PrecipProb    []float64 `json:"precipitation_probability"`
		WeatherCode   []int     `json:"weather_code"`
		CloudCover    []float64 `json:"cloud_cover"`
		WindSpeed     []float64 `json:"wind_speed_10m"`
	} `json:"hourly"`
}

// client talks to the Open-Meteo forecast and geocoding APIs.
type client struct {
	httpClient  *http.Client
	forecastURL string
	geocodeURL  string
}

func newClient() *client {
	return &client{
		httpClient:  &http.Client{Timeout: httpTimeout},
		forecastURL: defaultForecastURL,
		geocodeURL:  defaultGeocodeURL,
	}
}

func (c *client) getJSON(ctx context.Context, what, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("open-meteo %s: build request: %w", what, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("open-meteo %s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("open-meteo %s: status %s", what, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("open-meteo %s: read body: %w", what, err)
	}
	if len(body) > maxBodyBytes {
		return fmt.Errorf("open-meteo %s: %w", what, errBodyTooLarge)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("open-meteo %s: decode: %w", what, err)
	}
	return nil
}

// geocode resolves name to a place, preferring German results. ok is false
// when the API returns no results.
func (c *client) geocode(ctx context.Context, name string) (place, bool, error) {
	q := url.Values{}
	q.Set("name", name)
	q.Set("count", "10")
	q.Set("language", "de")
	q.Set("format", "json")
	var resp geocodeResponse
	if err := c.getJSON(ctx, "geocode", c.geocodeURL+"?"+q.Encode(), &resp); err != nil {
		return place{}, false, err
	}
	if len(resp.Results) == 0 {
		return place{}, false, nil
	}
	for _, p := range resp.Results {
		if p.CountryCode == "DE" {
			return p, true, nil
		}
	}
	return resp.Results[0], true, nil
}

// forecast fetches current, hourly and daily data for days days.
func (c *client) forecast(ctx context.Context, lat, lon float64, days int) (forecastResponse, error) {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(lat, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(lon, 'f', 4, 64))
	q.Set("timezone", "auto")
	q.Set("forecast_days", strconv.Itoa(days))
	q.Set("wind_speed_unit", "kmh")
	q.Set("daily", dailyVars)
	q.Set("hourly", hourlyVars)
	q.Set("current", currentVars)
	var resp forecastResponse
	if err := c.getJSON(ctx, "forecast", c.forecastURL+"?"+q.Encode(), &resp); err != nil {
		return forecastResponse{}, err
	}
	return resp, nil
}
