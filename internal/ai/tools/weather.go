package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const (
	geocodingURL = "https://geocoding-api.open-meteo.com/v1/search"
	forecastURL  = "https://api.open-meteo.com/v1/forecast"
)

type weatherArgs struct {
	Place string `json:"place"`
}

func weather(geocoding, forecast string) Tool {
	return Tool{
		Tool: aiTool("get_weather",
			"Current weather and the forecast for the next three days, for a city or place.",
			object([]string{"place"}, map[string]any{
				"place": stringParam("The city or place, in English, such as Paris or Kinshasa, DR Congo."),
			})),
		Status: func(args json.RawMessage) string {
			parsed, _ := arguments[weatherArgs](args)
			return "Checking the weather in " + parsed.Place
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			parsed, err := arguments[weatherArgs](args)
			if err != nil {
				return "", err
			}
			return forecastFor(ctx, geocoding, forecast, parsed.Place)
		},
	}
}

func forecastFor(ctx context.Context, geocoding, forecast, place string) (string, error) {
	var found struct {
		Results []placeMatch `json:"results"`
	}
	name, region, _ := strings.Cut(place, ",")
	if err := getJSON(ctx, geocoding+"?count=10&name="+url.QueryEscape(strings.TrimSpace(name)), &found); err != nil {
		return "", err
	}
	if len(found.Results) == 0 {
		return "No place called " + place + " was found.", nil
	}
	at := bestMatch(found.Results, region)

	var report struct {
		Timezone string `json:"timezone"`
		Current  struct {
			Time        string  `json:"time"`
			Temperature float64 `json:"temperature_2m"`
			FeelsLike   float64 `json:"apparent_temperature"`
			Humidity    float64 `json:"relative_humidity_2m"`
			Wind        float64 `json:"wind_speed_10m"`
			Code        int     `json:"weather_code"`
		} `json:"current"`
		Daily struct {
			Time []string  `json:"time"`
			Code []int     `json:"weather_code"`
			High []float64 `json:"temperature_2m_max"`
			Low  []float64 `json:"temperature_2m_min"`
			Rain []float64 `json:"precipitation_probability_max"`
		} `json:"daily"`
	}
	query := url.Values{
		"latitude":      {fmt.Sprint(at.Latitude)},
		"longitude":     {fmt.Sprint(at.Longitude)},
		"current":       {"temperature_2m,apparent_temperature,relative_humidity_2m,wind_speed_10m,weather_code"},
		"daily":         {"weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max"},
		"timezone":      {"auto"},
		"forecast_days": {"4"},
	}
	if err := getJSON(ctx, forecast+"?"+query.Encode(), &report); err != nil {
		return "", err
	}

	var text strings.Builder
	fmt.Fprintf(&text, "Weather in %s (%s), local time %s:\n", placeName(at.Name, at.Admin, at.Country), report.Timezone, report.Current.Time)
	fmt.Fprintf(&text, "Now: %s, %.0f°C (feels like %.0f°C), humidity %.0f%%, wind %.0f km/h\n",
		weatherCodes[report.Current.Code], report.Current.Temperature, report.Current.FeelsLike, report.Current.Humidity, report.Current.Wind)
	for i, day := range report.Daily.Time {
		if i >= len(report.Daily.Code) || i >= len(report.Daily.High) || i >= len(report.Daily.Low) || i >= len(report.Daily.Rain) {
			break
		}
		fmt.Fprintf(&text, "%s: %s, %.0f°C to %.0f°C, %.0f%% chance of rain\n",
			day, weatherCodes[report.Daily.Code[i]], report.Daily.Low[i], report.Daily.High[i], report.Daily.Rain[i])
	}
	return text.String(), nil
}

type placeMatch struct {
	Name        string  `json:"name"`
	Admin       string  `json:"admin1"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

// bestMatch is the first place in the region named after the comma, such as Texas in "Paris, Texas",
// and otherwise the most prominent place of that name.
func bestMatch(places []placeMatch, region string) placeMatch {
	region = strings.ToLower(strings.TrimSpace(region))
	if region != "" {
		for _, place := range places {
			for _, name := range []string{place.Admin, place.Country, place.CountryCode} {
				name = strings.ToLower(name)
				if name != "" && (strings.Contains(region, name) || strings.Contains(name, region)) {
					return place
				}
			}
		}
	}
	return places[0]
}

func placeName(parts ...string) string {
	var named []string
	for _, part := range parts {
		if part != "" {
			named = append(named, part)
		}
	}
	return strings.Join(named, ", ")
}

// weatherCodes are the WMO codes Open-Meteo reports.
var weatherCodes = map[int]string{
	0: "clear sky", 1: "mainly clear", 2: "partly cloudy", 3: "overcast",
	45: "fog", 48: "freezing fog",
	51: "light drizzle", 53: "drizzle", 55: "heavy drizzle", 56: "freezing drizzle", 57: "heavy freezing drizzle",
	61: "light rain", 63: "rain", 65: "heavy rain", 66: "freezing rain", 67: "heavy freezing rain",
	71: "light snow", 73: "snow", 75: "heavy snow", 77: "snow grains",
	80: "light showers", 81: "showers", 82: "violent showers", 85: "snow showers", 86: "heavy snow showers",
	95: "thunderstorm", 96: "thunderstorm with hail", 99: "thunderstorm with heavy hail",
}
