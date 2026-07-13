package tools

import (
	"context"
	"encoding/json"
	"strings"
)

// Severity is the restriction severity per user.
type Severity string

const (
	SeverityAvoid       Severity = "avoid"
	SeverityIntolerance Severity = "intolerance"
	SeverityAnaphylaxis Severity = "anaphylaxis"
)

// Restriction represents a single dietary restriction the user has on file.
// JSON tags match the OpenAI tool-call arguments layout.
type Restriction struct {
	Name     string   `json:"restriction"`
	Severity Severity `json:"severity"`
}

// PantryItem represents a pantry item available to the user.
type PantryItem struct {
	Name   string  `json:"name"`
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

// ALL_RESTRICTIONS lists every restriction the keyword engine understands.
// Keep in sync with the FORBIDDEN map in restrictions.go.
var ALL_RESTRICTIONS = []string{
	"vegan", "vegetarian", "pescatarian", "keto", "gluten-free",
	"dairy-free", "nut-free", "low-sodium", "halal", "kosher", "low-fodmap",
}

// computeNutritionArgs is the input schema for compute_nutrition.
type computeNutritionArgs struct {
	Ingredients []IngredientInput `json:"ingredients" jsonschema_description:"Ingredients with name, amount and unit"`
	Servings    int               `json:"servings" jsonschema:"minimum=1"`
}

// ComputeNutrition estimates calories and macros for a list of ingredients
// and a number of servings using the built-in per-100g table. It never
// invents numbers — the table in nutrition.go is the single source of truth.
func ComputeNutrition() *Tool {
	return &Tool{
		Name:        "compute_nutrition",
		Description: "Estimate calories and macros (protein, carbs, fat) for a list of ingredients and a number of servings. Use this to check whether a meal meets a calorie or macro target before finalising a recipe.",
		Parameters:  schemaFor(&computeNutritionArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in computeNutritionArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			grams := make([]gramsInput, 0, len(in.Ingredients))
			for _, i := range in.Ingredients {
				grams = append(grams, gramsInput{Name: i.Name, Grams: IngredientToGrams(i.Amount, i.Unit)})
			}
			out := EstimateNutrition(grams, in.Servings)
			b, _ := json.Marshal(out)
			return string(b), nil
		},
	}
}

// searchIngredientsArgs is the input schema for search_ingredients.
type searchIngredientsArgs struct {
	Query string `json:"query" jsonschema_description:"Ingredient or food name to search, e.g. 'chickpeas'"`
}

// SearchIngredients looks up per-100g nutrition for an ingredient. It queries
// the USDA FoodData Central API when USDA_API_KEY is set, otherwise it falls
// back to the built-in estimate table.
func SearchIngredients() *Tool {
	return &Tool{
		Name:        "search_ingredients",
		Description: "Search for foods/ingredients by name and return per-100g nutrition (calories, protein, carbs, fat). Use this to look up real nutrient values for an ingredient or to find options for a category (e.g. 'high protein beans'). Results come from the USDA FoodData Central API when configured, otherwise a built-in estimate table.",
		Parameters:  schemaFor(&searchIngredientsArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in searchIngredientsArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			results, source := searchIngredients(in.Query, 6)
			out := map[string]any{
				"query":   in.Query,
				"results": results,
				"source":  source,
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		},
	}
}

// ingredientResult mirrors the OG IngredientSearchResult shape.
type ingredientResult struct {
	Description  string   `json:"description"`
	Calories     *float64 `json:"calories,omitempty"`
	ProteinGrams *float64 `json:"proteinGrams,omitempty"`
	CarbsGrams   *float64 `json:"carbsGrams,omitempty"`
	FatGrams     *float64 `json:"fatGrams,omitempty"`
	Source       string   `json:"source"`
}

func fptr(f float64) *float64 { return &f }

func searchIngredients(query string, pageSize int) ([]ingredientResult, string) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []ingredientResult{}, "local-estimate"
	}
	if res, ok := usdaSearch(q, pageSize); ok {
		return res, "usda"
	}
	return localIngredientSearch(q, pageSize), "local-estimate"
}

func localIngredientSearch(query string, limit int) []ingredientResult {
	q := strings.ToLower(query)
	matches := KnownIngredients()
	out := make([]ingredientResult, 0, limit)
	for _, n := range matches {
		name, _ := n["name"].(string)
		if strings.Contains(name, q) || strings.Contains(q, name) {
			kcal, _ := n["kcal"].(float64)
			protein, _ := n["protein"].(float64)
			carbs, _ := n["carbs"].(float64)
			fat, _ := n["fat"].(float64)
			out = append(out, ingredientResult{
				Description:  name,
				Calories:     fptr(kcal),
				ProteinGrams: fptr(protein),
				CarbsGrams:   fptr(carbs),
				FatGrams:     fptr(fat),
				Source:       "local-estimate",
			})
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}
