package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/orionryan/diet-app-go/internal/ai/provider"
)

// GenerateRecipe finalises and returns a structured recipe (with computed
// nutrition). The agent should call it once it has reasoned about the
// request and (optionally) verified macros with compute_nutrition.
func GenerateRecipe() *Tool {
	return &Tool{
		Name:        "generate_recipe",
		Description: "Finalise and return a structured recipe. Call this once you have reasoned about the user's request, dietary restrictions and (optionally) verified macros with compute_nutrition. Pass the full recipe details.",
		Parameters:  schemaFor(&RecipeArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in RecipeArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			grams := make([]gramsInput, len(in.Ingredients))
			for i, ing := range in.Ingredients {
				grams[i] = gramsInput{Name: ing.Name, Grams: IngredientToGrams(ing.Amount, ing.Unit)}
			}
			nutrition := EstimateNutrition(grams, in.Servings)
			out := map[string]any{
				"id":          fmt.Sprintf("recipe_%s", strconv.FormatInt(time.Now().UnixNano(), 36)),
				"title":       in.Title,
				"description": in.Description,
				"servings":    in.Servings,
				"prepMinutes": in.PrepMinutes,
				"cookMinutes": in.CookMinutes,
				"ingredients": in.Ingredients,
				"steps":       in.Steps,
				"dietaryTags": in.DietaryTags,
				"nutrition":   nutrition,
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		},
	}
}

// CATEGORY_RULES organises grocery items (mirrors the OG CATEGORY_RULES).
var categoryRules = []struct {
	category string
	keywords []string
}{
	{"Produce", []string{"lettuce", "spinach", "kale", "tomato", "onion", "garlic", "carrot", "broccoli", "cauliflower", "pepper", "cucumber", "zucchini", "mushroom", "avocado", "lemon", "lime", "banana", "apple", "orange", "potato", "sweet potato", "herb", "parsley", "cilantro", "basil", "green onion", "celery", "cabbage", "eggplant", "asparagus", "corn"}},
	{"Meat & Seafood", []string{"chicken", "beef", "pork", "turkey", "lamb", "duck", "bacon", "ham", "sausage", "salmon", "tuna", "shrimp", "fish", "cod", "tilapia", "ground", "steak", "breast", "thigh"}},
	{"Dairy & Eggs", []string{"milk", "cream", "butter", "cheese", "yogurt", "egg", "eggs", "sour cream", "cream cheese", "mozzarella", "cheddar", "parmesan", "feta", "ricotta", "greek yogurt"}},
	{"Grains & Bread", []string{"rice", "pasta", "bread", "flour", "oat", "quinoa", "couscous", "noodle", "tortilla", "cereal", "cracker", "bagel", "wrap", "pita", "breadcrumb"}},
	{"Canned & Jarred", []string{"can", "canned", "jar", "tomato sauce", "tomato paste", "broth", "stock", "bean", "chickpea", "lentil", "tuna can", "coconut milk", "pickle", "olive"}},
	{"Spices & Seasonings", []string{"salt", "pepper", "cumin", "paprika", "turmeric", "cinnamon", "oregano", "thyme", "rosemary", "chili", "garlic powder", "onion powder", "bay leaf", "nutmeg", "ginger"}},
	{"Oils & Vinegars", []string{"olive oil", "coconut oil", "vegetable oil", "sesame oil", "vinegar", "balsamic", "cooking spray"}},
	{"Condiments & Sauces", []string{"soy sauce", "ketchup", "mustard", "mayonnaise", "hot sauce", "salsa", "honey", "maple syrup", "sriracha", "bbq sauce", "pasta sauce"}},
	{"Frozen", []string{"frozen", "ice cream"}},
	{"Baking", []string{"sugar", "baking powder", "baking soda", "vanilla", "cocoa", "chocolate chip", "yeast"}},
	{"Beverages", []string{"coffee", "tea", "juice", "water"}},
}

var categoryOrder = []string{"Produce", "Meat & Seafood", "Dairy & Eggs", "Grains & Bread", "Canned & Jarred", "Condiments & Sauces", "Spices & Seasonings", "Oils & Vinegars", "Frozen", "Baking", "Beverages", "Other"}

func categorizeIngredient(name string) string {
	lower := strings.ToLower(name)
	for _, rule := range categoryRules {
		for _, kw := range rule.keywords {
			if strings.Contains(lower, kw) {
				return rule.category
			}
		}
	}
	return "Other"
}

func normalizeIngredientName(name string) string {
	n := strings.TrimSpace(strings.ToLower(name))
	if strings.HasSuffix(n, "s") && len(n) > 1 {
		n = n[:len(n)-1]
	}
	return n
}

// groceryRecipe is a single recipe input for build_grocery_list.
type groceryRecipe struct {
	Title       string            `json:"title"`
	Ingredients []IngredientInput `json:"ingredients"`
}

// buildGroceryListArgs is the input schema for build_grocery_list.
type buildGroceryListArgs struct {
	Recipes []groceryRecipe `json:"recipes" jsonschema_description:"The recipes to build a grocery list from"`
}

// BuildGroceryList deduplicates, totals, and categorises ingredients from one
// or more recipes into a shopping list.
func BuildGroceryList() *Tool {
	return &Tool{
		Name:        "build_grocery_list",
		Description: "Build a consolidated, categorised grocery list from one or more recipes. Call this after generating multiple recipes for a meal plan. It deduplicates ingredients, adds up amounts, and organises items by category (Produce, Meat, Dairy, Pantry, etc.) for easy shopping.",
		Parameters:  schemaFor(&buildGroceryListArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in buildGroceryListArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			type merged struct {
				amount      float64
				unit        string
				fromRecipes map[string]bool
			}
			m := map[string]*merged{}
			for _, r := range in.Recipes {
				for _, ing := range r.Ingredients {
					key := normalizeIngredientName(ing.Name)
					if existing, ok := m[key]; ok {
						existing.amount += ing.Amount
						existing.fromRecipes[r.Title] = true
					} else {
						m[key] = &merged{
							amount:      ing.Amount,
							unit:        ing.Unit,
							fromRecipes: map[string]bool{r.Title: true},
						}
					}
				}
			}
			catMap := map[string][]map[string]any{}
			for name, data := range m {
				cat := categorizeIngredient(name)
				from := make([]string, 0, len(data.fromRecipes))
				for f := range data.fromRecipes {
					from = append(from, f)
				}
				catMap[cat] = append(catMap[cat], map[string]any{
					"name":        name,
					"amount":      math.Round(data.amount*10) / 10,
					"unit":        data.unit,
					"fromRecipes": from,
				})
			}
			categories := make([]map[string]any, 0, len(catMap))
			for _, cat := range categoryOrder {
				items, ok := catMap[cat]
				if !ok {
					continue
				}
				sortByName(items)
				categories = append(categories, map[string]any{"category": cat, "items": items})
			}
			out := map[string]any{
				"categories": categories,
				"totalItems": len(m),
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		},
	}
}

func sortByName(items []map[string]any) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j-1]["name"].(string) > items[j]["name"].(string); j-- {
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
}

// --- LLM-backed tools ---

// llmJSON asks the model for a JSON object and extracts it from the reply.
func llmJSON(ctx context.Context, client *provider.Client, system, user string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("no model client available")
	}
	resp, err := client.Chat(ctx, provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("%s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return extractJSON(resp.Choices[0].Message.Content), nil
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end < start {
		return s
	}
	return s[start : end+1]
}

// ValidateIngredientsDeep is an LLM-powered second pass that catches hidden
// allergens the keyword engine misses. Falls back to keyword checking when the
// model call or JSON parse fails.
func ValidateIngredientsDeep(client *provider.Client) *Tool {
	return &Tool{
		Name:        "validate_ingredients_deep",
		Description: "Deep AI-powered ingredient validation that catches hidden allergens and derived ingredients the keyword-based apply_restrictions tool misses. Use this AFTER apply_restrictions when the user has an intolerance or anaphylaxis-level restriction, or when an ingredient name is ambiguous (e.g. 'broth', 'sauce', 'breading', 'stock', 'flavoring'). Identifies hidden gluten in soy sauce/broth/breading, hidden dairy in processed foods, hidden nuts in sauces, and cross-contamination risks for anaphylaxis.",
		Parameters:  schemaFor(&validateDeepArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in validateDeepArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			restrictions := toRestrictions(in.Restrictions)
			system := "You are a food safety and allergen expert. Return ONLY a strict JSON object (no prose, no markdown fences) with this shape: " +
				`{"violations":[{"ingredient":string,"restriction":string,"severity":"avoid|intolerance|anaphylaxis","reason":string,"hiddenSource":string}],"warnings":[{"ingredient":string,"concern":string}],"allClear":boolean,"summary":string}.`
			user := fmt.Sprintf("Check these ingredients against the restrictions.\nIngredients: %s\nRestrictions: %s\n\nPay special attention to hidden gluten (broth, stock, soy sauce, breading, breadcrumbs, flavoring, malt, seitan, couscous), hidden dairy (whey, casein, lactose, ghee, cream sauces, margarines), hidden nuts (pesto, sauces, marzipan, almond flour), hidden eggs (mayo, meringue, lecithin), hidden animal products (gelatin, rennet, honey, stock), and cross-contamination for anaphylaxis. For anaphylaxis, flag ANY possible trace. For intolerance, flag hidden/derived sources. For avoid, standard category checking.",
				mustJSON(in.Ingredients), mustJSON(restrictions))
			raw, err := llmJSON(ctx, client, system, user)
			if err != nil {
				return keywordDeepFallback(in.Ingredients, restrictions), nil
			}
			var parsed struct {
				Violations []map[string]any `json:"violations"`
				Warnings   []map[string]any `json:"warnings"`
				AllClear   bool             `json:"allClear"`
				Summary    string           `json:"summary"`
			}
			if jErr := json.Unmarshal([]byte(raw), &parsed); jErr != nil {
				return keywordDeepFallback(in.Ingredients, restrictions), nil
			}
			b, _ := json.Marshal(parsed)
			return string(b), nil
		},
	}
}

func keywordDeepFallback(ingredients []string, restrictions []Restriction) string {
	check := applyRestrictions(ingredients, restrictions)
	violations := make([]map[string]any, 0, len(check.Violations))
	for _, v := range check.Violations {
		violations = append(violations, map[string]any{
			"ingredient":  v.Ingredient,
			"restriction": v.Restriction,
			"severity":    string(v.Severity),
			"reason":      v.Reason,
		})
	}
	out := map[string]any{
		"violations": violations,
		"warnings":   []any{},
		"allClear":   check.AllClear,
		"summary":    "Deep validation unavailable — keyword check only.",
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var dayLabels = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// mealSlot is a single meal slot for plan_week.
type mealSlot struct {
	Day           int    `json:"day" jsonschema:"minimum=0,maximum=6" jsonschema_description:"Day of week: 0=Monday, 6=Sunday"`
	MealType      string `json:"mealType" jsonschema:"enum=breakfast,enum=lunch,enum=dinner,enum=snack"`
	Description   string `json:"description" jsonschema_description:"Brief description of what this meal should be"`
	CalorieTarget int    `json:"calorieTarget,omitempty" jsonschema_description:"Target calories for this meal"`
}

// planWeekArgs is the input schema for plan_week.
type planWeekArgs struct {
	Meals []mealSlot `json:"meals"`
	Notes string     `json:"notes,omitempty" jsonschema_description:"General notes or constraints for the whole plan"`
}

// PlanWeek generates a full multi-day meal plan with complete recipes for each
// meal slot, using one batched structured LLM call. Falls back to an empty
// recipe list on failure so the agent can fill slots in one by one.
func PlanWeek(client *provider.Client) *Tool {
	return &Tool{
		Name:        "plan_week",
		Description: "Generate a full multi-day meal plan with complete recipes for each meal slot. Call this when the user asks for a weekly meal plan, 3-day plan, etc. The tool returns a list of recipes, one per requested slot, ready to assign to the meal plan calendar. Optionally call build_grocery_list afterwards with the returned recipes.",
		Parameters:  schemaFor(&planWeekArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in planWeekArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			slots := in.Meals
			if len(slots) > 28 {
				slots = slots[:28]
			}
			slotList := make([]string, 0, len(slots))
			for i, m := range slots {
				d := m.Day
				if d < 0 || d > 6 {
					d = 0
				}
				line := fmt.Sprintf("%d. Day %d (%s), %s", i+1, m.Day, dayLabels[d], m.MealType)
				if m.CalorieTarget > 0 {
					line += fmt.Sprintf(", ~%d kcal", m.CalorieTarget)
				}
				line += ": " + m.Description
				slotList = append(slotList, line)
			}
			system := "You are planning a meal plan. Return ONLY a strict JSON object (no prose, no markdown fences) with this shape: " +
				`{"recipes":[` +
				`{"title":string,"description":string,"servings":int,"prepMinutes":int,"cookMinutes":int,` +
				`"ingredients":[{"name":string,"amount":number,"unit":string}],"steps":[string],"dietaryTags":[string]}` +
				`]}. Return exactly one recipe per slot, in order.`
			user := fmt.Sprintf("Generate a complete recipe for each of the following meal slots.\n\n%s\n\n%sBe realistic with portions, common units (g, ml, cup, tbsp, tsp, piece, can, handful), and brief ordered steps. Respect any dietary restrictions already in the agent's context.",
				strings.Join(slotList, "\n"),
				ternaryStr(len(in.Notes) > 0, "Plan-wide notes: "+in.Notes+"\n", ""))

			raw, err := llmJSON(ctx, client, system, user)
			if err != nil {
				return mustJSON(map[string]any{"dayLabels": dayLabels, "notes": in.Notes, "recipes": []any{}}), nil
			}
			var parsed struct {
				Recipes []struct {
					Title       string `json:"title"`
					Description string `json:"description"`
					Servings    int    `json:"servings"`
					PrepMinutes int    `json:"prepMinutes"`
					CookMinutes int    `json:"cookMinutes"`
					Ingredients []struct {
						Name   string  `json:"name"`
						Amount float64 `json:"amount"`
						Unit   string  `json:"unit"`
					} `json:"ingredients"`
					Steps       []string `json:"steps"`
					DietaryTags []string `json:"dietaryTags"`
				} `json:"recipes"`
			}
			if jErr := json.Unmarshal([]byte(raw), &parsed); jErr != nil {
				return mustJSON(map[string]any{"dayLabels": dayLabels, "notes": in.Notes, "recipes": []any{}}), nil
			}
			pairs := make([]map[string]any, 0, len(slots))
			for i, slot := range slots {
				if i >= len(parsed.Recipes) {
					break
				}
				r := parsed.Recipes[i]
				grams := make([]gramsInput, len(r.Ingredients))
				for j, ing := range r.Ingredients {
					grams[j] = gramsInput{Name: ing.Name, Grams: IngredientToGrams(ing.Amount, ing.Unit)}
				}
				nutrition := EstimateNutrition(grams, r.Servings)
				pairs = append(pairs, map[string]any{
					"day":      slot.Day,
					"mealType": slot.MealType,
					"recipe": map[string]any{
						"id":          fmt.Sprintf("recipe_%s_%d", strconv.FormatInt(time.Now().UnixNano(), 36), i),
						"title":       r.Title,
						"description": r.Description,
						"servings":    r.Servings,
						"prepMinutes": r.PrepMinutes,
						"cookMinutes": r.CookMinutes,
						"ingredients": r.Ingredients,
						"steps":       r.Steps,
						"dietaryTags": r.DietaryTags,
						"nutrition":   nutrition,
					},
				})
			}
			out := map[string]any{
				"dayLabels": dayLabels,
				"notes":     in.Notes,
				"recipes":   pairs,
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		},
	}
}

func ternaryStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
