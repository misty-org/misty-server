package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const toolboxWeatherCurrent = "weather.current"

var weatherHTTPClient = &http.Client{Timeout: 10 * time.Second}

func currentWeather(ctx context.Context, location string) (json.RawMessage, error) {
	location = strings.TrimSpace(location)
	if location == "" || len([]rune(location)) > 240 {
		return nil, errors.New("weather location is required")
	}
	geocodingURL, _ := weatherRequestURLs(location, 0, 0)
	var geocoding struct {
		Results []struct {
			Name      string  `json:"name"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Timezone  string  `json:"timezone"`
			Country   string  `json:"country"`
			Admin1    string  `json:"admin1"`
		} `json:"results"`
	}
	if err := weatherGET(ctx, geocodingURL, &geocoding); err != nil {
		return nil, err
	}
	if len(geocoding.Results) == 0 {
		return nil, errors.New("weather location was not found")
	}
	place := geocoding.Results[0]
	_, forecastURL := weatherRequestURLs(location, place.Latitude, place.Longitude)
	var forecast struct {
		Timezone string         `json:"timezone"`
		Current  map[string]any `json:"current"`
		Units    map[string]any `json:"current_units"`
	}
	if err := weatherGET(ctx, forecastURL, &forecast); err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(map[string]any{
		"location": map[string]any{
			"name": place.Name, "region": place.Admin1, "country": place.Country,
			"latitude": place.Latitude, "longitude": place.Longitude,
		},
		"timezone": forecast.Timezone, "current": forecast.Current, "units": forecast.Units,
		"provider": "Open-Meteo", "observed_at": forecast.Current["time"],
	}), nil
}

func weatherRequestURLs(location string, latitude, longitude float64) (string, string) {
	geocoding := url.URL{Scheme: "https", Host: "geocoding-api.open-meteo.com", Path: "/v1/search"}
	query := geocoding.Query()
	query.Set("name", strings.TrimSpace(location))
	query.Set("count", "1")
	query.Set("language", "en")
	query.Set("format", "json")
	geocoding.RawQuery = query.Encode()
	forecast := url.URL{Scheme: "https", Host: "api.open-meteo.com", Path: "/v1/forecast"}
	query = forecast.Query()
	query.Set("latitude", strconv.FormatFloat(latitude, 'f', 6, 64))
	query.Set("longitude", strconv.FormatFloat(longitude, 'f', 6, 64))
	query.Set("current", "temperature_2m,apparent_temperature,relative_humidity_2m,weather_code,wind_speed_10m")
	query.Set("temperature_unit", "fahrenheit")
	query.Set("wind_speed_unit", "mph")
	query.Set("timezone", "auto")
	forecast.RawQuery = query.Encode()
	return geocoding.String(), forecast.String()
}

func weatherGET(ctx context.Context, endpoint string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := weatherHTTPClient.Do(request)
	if err != nil {
		return errors.New("live weather is temporarily unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("live weather provider returned status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || len(body) == 1<<20 {
		return errors.New("live weather response was invalid")
	}
	if json.Unmarshal(body, output) != nil {
		return errors.New("live weather response was invalid")
	}
	return nil
}

func TestingWeatherRequestURLs(location string, latitude, longitude float64) (string, string) {
	return weatherRequestURLs(location, latitude, longitude)
}
