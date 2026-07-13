package tools

import "github.com/invopop/jsonschema"

// RestrictionName is a dietary restriction name. Its JSONSchema method emits
// the enum of valid restrictions from ALL_RESTRICTIONS, keeping a single
// source of truth for schema + runtime.
type RestrictionName string

func (RestrictionName) JSONSchema() *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "string"}
	for _, r := range ALL_RESTRICTIONS {
		s.Enum = append(s.Enum, r)
	}
	return s
}

// JSONSchema emits the severity enum for Severity-typed schema fields.
func (Severity) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "string",
		Enum: []any{"avoid", "intolerance", "anaphylaxis"},
	}
}

// IngredientInput is the shared ingredient shape used across recipe/nutrition
// tools.
type IngredientInput struct {
	Name   string  `json:"name" jsonschema_description:"Ingredient name, e.g. 'chicken breast'"`
	Amount float64 `json:"amount" jsonschema_description:"Numeric quantity, e.g. 200"`
	Unit   string  `json:"unit" jsonschema_description:"Unit such as g, ml, cup, tbsp, tsp, oz, lb, piece, can, handful"`
}

// restrictionInput is the tool-facing restriction entry (with enum-typed
// fields). It is converted to the internal Restriction type inside Run.
type restrictionInput struct {
	Restriction RestrictionName `json:"restriction"`
	Severity    Severity        `json:"severity"`
}

func toRestrictions(in []restrictionInput) []Restriction {
	out := make([]Restriction, 0, len(in))
	for _, r := range in {
		out = append(out, Restriction{Name: string(r.Restriction), Severity: r.Severity})
	}
	return out
}

// validateDeepArgs is the input schema for validate_ingredients_deep.
type validateDeepArgs struct {
	Ingredients  []string           `json:"ingredients"`
	Restrictions []restrictionInput `json:"restrictions"`
}

// RecipeArgs is shared by generate_recipe and save_recipe_to_library.
type RecipeArgs struct {
	Title       string            `json:"title"`
	Description string            `json:"description" jsonschema_description:"One or two sentence summary of the dish"`
	Servings    int               `json:"servings" jsonschema:"minimum=1"`
	PrepMinutes int               `json:"prepMinutes" jsonschema:"minimum=0"`
	CookMinutes int               `json:"cookMinutes" jsonschema:"minimum=0"`
	Ingredients []IngredientInput `json:"ingredients"`
	Steps       []string          `json:"steps" jsonschema_description:"Ordered cooking steps"`
	DietaryTags []string          `json:"dietaryTags" jsonschema_description:"e.g. vegan, vegetarian, keto, gluten-free, high-protein"`
}
