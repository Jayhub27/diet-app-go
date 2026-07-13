package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

// PlaceResult mirrors the OG PlaceResult shape.
type PlaceResult struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
	MapsURI string `json:"mapsUri,omitempty"`
}

// SearchLocations finds nearby grocery stores/markets via the Google Places
// API (v1) when GOOGLE_MAPS_API_KEY is set; otherwise it returns an empty
// result with fallback=true so the agent can still give practical advice.
func SearchLocations() *Tool {
	return &Tool{
		Name:        "search_locations",
		Description: "Find nearby grocery stores, markets or shops where an ingredient can be bought. Accepts a query (e.g. 'grocery stores', 'asian market') and optionally the user's latitude/longitude and a radius in meters. Uses the Google Places API when configured; otherwise returns a fallback hint so you can still give the user practical advice.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":        map[string]any{"type": "string", "description": "Place query, e.g. 'grocery stores near me' or 'farmers market'"},
				"latitude":     map[string]any{"type": "number", "description": "User latitude for nearby search"},
				"longitude":    map[string]any{"type": "number", "description": "User longitude for nearby search"},
				"radiusMeters": map[string]any{"type": "integer", "description": "Search radius in meters"},
			},
			"required": []string{"query"},
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Query        string  `json:"query"`
				Latitude     float64 `json:"latitude"`
				Longitude    float64 `json:"longitude"`
				RadiusMeters int     `json:"radiusMeters"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			results, fallback := searchLocations(in.Query, in.Latitude, in.Longitude, in.RadiusMeters)
			out := map[string]any{
				"query":    in.Query,
				"results":  results,
				"fallback": fallback,
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		},
	}
}

func searchLocations(query string, lat, lng float64, radius int) ([]PlaceResult, bool) {
	apiKey := os.Getenv("GOOGLE_MAPS_API_KEY")
	if apiKey == "" {
		return []PlaceResult{}, true
	}

	body := map[string]any{
		"textQuery":    query,
		"languageCode": "en",
	}
	if lat != 0 && lng != 0 {
		r := radius
		if r == 0 {
			r = 5000
		}
		body["locationBias"] = map[string]any{
			"circle": map[string]any{
				"center": map[string]any{"latitude": lat, "longitude": lng},
				"radius": r,
			},
		}
	}
	payload, _ := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPost, "https://places.googleapis.com/v1/places:searchText", strings.NewReader(string(payload)))
	if err != nil {
		return []PlaceResult{}, true
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", apiKey)
	req.Header.Set("X-Goog-FieldMask", "places.displayName,places.formattedAddress,places.googleMapsUri")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return []PlaceResult{}, true
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return []PlaceResult{}, true
	}
	var data struct {
		Places []struct {
			DisplayName struct {
				Text string `json:"text"`
			} `json:"displayName"`
			FormattedAddress string `json:"formattedAddress"`
			GoogleMapsURI    string `json:"googleMapsUri"`
		} `json:"places"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return []PlaceResult{}, true
	}
	results := make([]PlaceResult, 0, len(data.Places))
	for _, p := range data.Places {
		results = append(results, PlaceResult{
			Name:    p.DisplayName.Text,
			Address: p.FormattedAddress,
			MapsURI: p.GoogleMapsURI,
		})
	}
	return results, false
}
