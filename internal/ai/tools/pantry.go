package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// pantryItemInput is a single item for add_to_pantry (amount/unit optional).
type pantryItemInput struct {
	Name   string  `json:"name" jsonschema_description:"Ingredient name, e.g. 'chicken breast'"`
	Amount float64 `json:"amount,omitempty" jsonschema_description:"Quantity, e.g. 500"`
	Unit   string  `json:"unit,omitempty" jsonschema_description:"Unit such as g, ml, cup, piece"`
}

// addToPantryArgs is the input schema for add_to_pantry.
type addToPantryArgs struct {
	Items []pantryItemInput `json:"items"`
}

// AddToPantry adds one or more ingredients to the user's pantry.
func AddToPantry(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "add_to_pantry",
		Description: "Add one or more ingredients to the user's pantry. Use this when the user says 'add X to my pantry', 'I just bought Y', or when building a grocery list and the user confirms they have certain items. Each item needs a name and optionally an amount and unit.",
		Parameters:  schemaFor(&addToPantryArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			if db == nil || userID == "" {
				return mustJSON(map[string]any{"added": []any{}, "error": "Sign in to use the pantry feature."}), nil
			}
			var in addToPantryArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			added := make([]map[string]any, 0, len(in.Items))
			for _, item := range in.Items {
				if strings.TrimSpace(item.Name) == "" {
					continue
				}
				_, err := db.ExecContext(ctx,
					`insert into pantry (id, user_id, name, amount, unit) values (?, ?, ?, ?, ?)`,
					uuid.NewString(), userID, item.Name, item.Amount, item.Unit)
				if err != nil {
					return mustJSON(map[string]any{"added": added, "error": err.Error()}), nil
				}
				added = append(added, map[string]any{"name": item.Name, "amount": item.Amount, "unit": item.Unit})
			}
			return mustJSON(map[string]any{"added": added, "error": nil}), nil
		},
	}
}

// removeFromPantryArgs is the input schema for remove_from_pantry.
type removeFromPantryArgs struct {
	Names []string `json:"names" jsonschema_description:"Ingredient names to remove"`
}

// RemoveFromPantry removes ingredients from the user's pantry by name.
func RemoveFromPantry(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "remove_from_pantry",
		Description: "Remove an ingredient from the user's pantry by name. Use this when the user says 'remove X from my pantry', 'I used up the Y', or 'I don't have Z anymore'. Matches by ingredient name (case-insensitive).",
		Parameters:  schemaFor(&removeFromPantryArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			if db == nil || userID == "" {
				return mustJSON(map[string]any{"removed": []any{}, "error": "Sign in to use the pantry feature."}), nil
			}
			var in removeFromPantryArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			rows, err := db.QueryContext(ctx,
				`select id, name from pantry where user_id = ?`, userID)
			if err != nil {
				return mustJSON(map[string]any{"removed": []any{}, "error": err.Error()}), nil
			}
			owned := map[string]string{}
			for rows.Next() {
				var id, name string
				if err := rows.Scan(&id, &name); err != nil {
					rows.Close()
					return mustJSON(map[string]any{"removed": []any{}, "error": err.Error()}), nil
				}
				owned[name] = id
			}
			rows.Close()

			removed := make([]string, 0)
			for _, name := range in.Names {
				for stored, id := range owned {
					if strings.EqualFold(stored, name) {
						if _, err := db.ExecContext(ctx, `delete from pantry where id = ?`, id); err != nil {
							return mustJSON(map[string]any{"removed": removed, "error": err.Error()}), nil
						}
						removed = append(removed, stored)
						break
					}
				}
			}
			return mustJSON(map[string]any{"removed": removed, "error": nil}), nil
		},
	}
}

// listPantryArgs is the (empty) input schema for list_pantry.
type listPantryArgs struct{}

// ListPantry lists everything in the user's pantry.
func ListPantry(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "list_pantry",
		Description: "List all ingredients currently in the user's pantry. Use this when the user asks 'what's in my pantry?', 'what do I have?', or before suggesting a recipe to check what ingredients are available.",
		Parameters:  schemaFor(&listPantryArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			if db == nil || userID == "" {
				return mustJSON(map[string]any{"items": []any{}, "error": "Sign in to use the pantry feature."}), nil
			}
			rows, err := db.QueryContext(ctx,
				`select name, coalesce(amount, 0), coalesce(unit, '') from pantry where user_id = ? order by created_at desc`, userID)
			if err != nil {
				return mustJSON(map[string]any{"items": []any{}, "error": err.Error()}), nil
			}
			defer rows.Close()
			items := make([]map[string]any, 0)
			for rows.Next() {
				var name string
				var amount float64
				var unit string
				if err := rows.Scan(&name, &amount, &unit); err != nil {
					return mustJSON(map[string]any{"items": []any{}, "error": err.Error()}), nil
				}
				items = append(items, map[string]any{"name": name, "amount": amount, "unit": unit})
			}
			return mustJSON(map[string]any{"items": items, "error": nil}), nil
		},
	}
}

// SaveRecipeToLibrary persists a generated recipe to the user's library.
func SaveRecipeToLibrary(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "save_recipe_to_library",
		Description: "Save a recipe to the user's saved recipe library. Use this when the user says 'save this recipe', 'add this to my library', or 'save for later'. The recipe must have been generated with generate_recipe first. Pass the full recipe details.",
		Parameters:  schemaFor(&RecipeArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			if db == nil || userID == "" {
				var in RecipeArgs
				_ = json.Unmarshal(args, &in)
				return mustJSON(map[string]any{"saved": false, "title": in.Title, "error": "Sign in to save recipes to your library."}), nil
			}
			var in RecipeArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			grams := make([]gramsInput, len(in.Ingredients))
			for i, ing := range in.Ingredients {
				grams[i] = gramsInput{Name: ing.Name, Grams: IngredientToGrams(ing.Amount, ing.Unit)}
			}
			nutrition := EstimateNutrition(grams, in.Servings)
			ingredientsJSON, _ := json.Marshal(in.Ingredients)
			stepsJSON, _ := json.Marshal(in.Steps)
			tagsJSON, _ := json.Marshal(in.DietaryTags)
			nutritionJSON, _ := json.Marshal(nutrition)
			_, err := db.ExecContext(ctx,
				`insert into recipes (id, user_id, title, description, servings, prep_minutes, cook_minutes, ingredients_json, steps_json, dietary_tags_json, nutrition_json, source_prompt)
				 values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'agent')`,
				uuid.NewString(), userID, in.Title, in.Description, in.Servings, in.PrepMinutes, in.CookMinutes,
				string(ingredientsJSON), string(stepsJSON), string(tagsJSON), string(nutritionJSON))
			if err != nil {
				return mustJSON(map[string]any{"saved": false, "title": in.Title, "error": err.Error()}), nil
			}
			return mustJSON(map[string]any{"saved": true, "title": in.Title, "error": nil}), nil
		},
	}
}
