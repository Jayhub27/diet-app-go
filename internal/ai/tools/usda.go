package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

// usdaFood mirrors the subset of the USDA FoodData Central search response we
// need.
type usdaFood struct {
	Description   string `json:"description"`
	FoodNutrients []struct {
		NutrientID int     `json:"nutrientId"`
		Value      float64 `json:"value"`
	} `json:"foodNutrients"`
}

// usdaSearch queries the USDA FoodData Central API. It returns ok=false when
// no API key is configured or the request fails, so callers can fall back to
// the local table.
func usdaSearch(query string, pageSize int) ([]ingredientResult, bool) {
	key := os.Getenv("USDA_API_KEY")
	if key == "" {
		return nil, false
	}
	if pageSize <= 0 {
		pageSize = 6
	}
	u := "https://api.nal.usda.gov/fdc/v1/foods/search"
	reqURL, _ := url.Parse(u)
	q := reqURL.Query()
	q.Set("api_key", key)
	q.Set("query", query)
	q.Set("pageSize", fmt.Sprintf("%d", pageSize))
	reqURL.RawQuery = q.Encode()

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(reqURL.String())
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var data struct {
		Foods []usdaFood `json:"foods"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, false
	}
	out := make([]ingredientResult, 0, len(data.Foods))
	for _, f := range data.Foods {
		out = append(out, ingredientResult{
			Description:  f.Description,
			Calories:     usdaNutrient(f, 1008),
			ProteinGrams: usdaNutrient(f, 1003),
			CarbsGrams:   usdaNutrient(f, 1005),
			FatGrams:     usdaNutrient(f, 1004),
			Source:       "usda",
		})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// usdaNutrient extracts a nutrient value and rounds it to 1 decimal. Returns
// nil when the nutrient is absent.
func usdaNutrient(f usdaFood, id int) *float64 {
	for _, n := range f.FoodNutrients {
		if n.NutrientID == id && n.Value != 0 {
			return fptr(round1(n.Value))
		}
	}
	return nil
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
