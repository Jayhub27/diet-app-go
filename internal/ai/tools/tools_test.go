package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func mustToolJSON(t *testing.T, tool *Tool, args string) map[string]any {
	t.Helper()
	out, err := tool.Run(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("tool did not return valid JSON: %v\nraw=%s", err, out)
	}
	return m
}

func TestComputeNutrition(t *testing.T) {
	m := mustToolJSON(t, ComputeNutrition(),
		`{"ingredients":[{"name":"chicken breast","amount":200,"unit":"g"},{"name":"olive oil","amount":1,"unit":"tbsp"}],"servings":2}`)
	total, ok := m["total"].(map[string]any)
	if !ok {
		t.Fatalf("missing total: %v", m)
	}
	if total["calories"] == nil {
		t.Fatalf("missing calories: %v", total)
	}
}

func TestGenerateRecipe(t *testing.T) {
	m := mustToolJSON(t, GenerateRecipe(),
		`{"title":"Test","description":"d","servings":2,"prepMinutes":5,"cookMinutes":10,"ingredients":[{"name":"rice","amount":100,"unit":"g"}],"steps":["boil"],"dietaryTags":["vegan"]}`)
	if m["id"] == nil || m["nutrition"] == nil {
		t.Fatalf("recipe missing id/nutrition: %v", m)
	}
}

func TestSearchIngredientsLocal(t *testing.T) {
	m := mustToolJSON(t, SearchIngredients(), `{"query":"chick"}`)
	results, ok := m["results"].([]any)
	if !ok || len(results) == 0 {
		t.Fatalf("expected results: %v", m)
	}
	if m["source"] != "local-estimate" {
		t.Fatalf("expected local-estimate source, got %v", m["source"])
	}
}

func TestApplyRestrictionsVegan(t *testing.T) {
	m := mustToolJSON(t, ApplyRestrictions(nil),
		`{"ingredients":["chicken breast","broccoli"],"restrictions":[{"restriction":"vegan","severity":"avoid"}]}`)
	if m["allClear"] != false {
		t.Fatalf("expected violations: %v", m)
	}
	viol, ok := m["violations"].([]any)
	if !ok || len(viol) == 0 {
		t.Fatalf("expected at least one violation: %v", m)
	}
	if m["maxSeverity"] != "avoid" {
		t.Fatalf("expected maxSeverity avoid: %v", m)
	}
}

func TestBuildGroceryList(t *testing.T) {
	m := mustToolJSON(t, BuildGroceryList(),
		`{"recipes":[{"title":"A","ingredients":[{"name":"chicken breast","amount":200,"unit":"g"},{"name":"onion","amount":1,"unit":"piece"}]},{"title":"B","ingredients":[{"name":"onions","amount":2,"unit":"piece"}]}]}`)
	cats, ok := m["categories"].([]any)
	if !ok || len(cats) == 0 {
		t.Fatalf("expected categories: %v", m)
	}
	total, _ := m["totalItems"].(float64)
	if int(total) != 2 {
		t.Fatalf("expected 2 merged items, got %v", total)
	}
}

func TestValidateDeepFallback(t *testing.T) {
	// No API reachable -> must fall back to keyword check without erroring.
	tool := ValidateIngredientsDeep(nil)
	m := mustToolJSON(t, tool,
		`{"ingredients":["chicken"],"restrictions":[{"restriction":"vegan","severity":"avoid"}]}`)
	if m["summary"] == nil {
		t.Fatalf("expected summary: %v", m)
	}
}

func TestPlanWeekFallback(t *testing.T) {
	tool := PlanWeek(nil)
	m := mustToolJSON(t, tool,
		`{"meals":[{"day":0,"mealType":"dinner","description":"veggie bowl"}],"notes":""}`)
	if m["dayLabels"] == nil {
		t.Fatalf("expected dayLabels: %v", m)
	}
	recipes, ok := m["recipes"].([]any)
	if !ok {
		t.Fatalf("expected recipes array: %v", m)
	}
	// Fallback returns empty list, not an error.
	if len(recipes) != 0 {
		t.Fatalf("expected empty fallback recipes, got %v", recipes)
	}
}

func setupPantryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`create table pantry (id text primary key, user_id text not null, name text not null, amount real, unit text, created_at text)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`create table recipes (id text primary key, user_id text not null, title text not null, description text, servings integer, prep_minutes integer, cook_minutes integer, ingredients_json text not null, steps_json text not null, dietary_tags_json text not null, nutrition_json text not null, source_prompt text)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPantryFlow(t *testing.T) {
	db := setupPantryDB(t)
	uid := "u1"
	add := mustToolJSON(t, AddToPantry(db, uid),
		`{"items":[{"name":"eggs","amount":6,"unit":"piece"},{"name":"milk","amount":1,"unit":"l"}]}`)
	added, _ := add["added"].([]any)
	if len(added) != 2 {
		t.Fatalf("expected 2 added: %v", add)
	}
	list := mustToolJSON(t, ListPantry(db, uid), `{}`)
	items, _ := list["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 listed: %v", list)
	}
	rm := mustToolJSON(t, RemoveFromPantry(db, uid), `{"names":["eggs"]}`)
	removed, _ := rm["removed"].([]any)
	if len(removed) != 1 {
		t.Fatalf("expected 1 removed: %v", rm)
	}
	save := mustToolJSON(t, SaveRecipeToLibrary(db, uid),
		`{"title":"R","description":"d","servings":2,"prepMinutes":1,"cookMinutes":2,"ingredients":[{"name":"rice","amount":100,"unit":"g"}],"steps":["cook"],"dietaryTags":["vegan"]}`)
	if save["saved"] != true {
		t.Fatalf("expected saved: %v", save)
	}
}

func TestSearchLocationsFallback(t *testing.T) {
	// No GOOGLE_MAPS_API_KEY -> fallback true, empty results.
	m := mustToolJSON(t, SearchLocations(), `{"query":"grocery stores"}`)
	if m["fallback"] != true {
		t.Fatalf("expected fallback true: %v", m)
	}
}

// TestReflectionSchemas verifies the reflection-generated JSON schemas are
// provider-compatible (no $ref/$defs) and emit enums for restriction/severity.
func TestReflectionSchemas(t *testing.T) {
	tools := []*Tool{
		ApplyRestrictions(nil),
		GenerateRecipe(),
		AddToPantry(nil, ""),
		RemoveFromPantry(nil, ""),
		BuildGroceryList(),
	}
	for _, tool := range tools {
		_, _, schema := tool.Schema()
		raw, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("%s: schema not JSON-marshalable: %v", tool.Name, err)
		}
		body := string(raw)
		if body == "" || body == "null" {
			t.Fatalf("%s: empty schema", tool.Name)
		}
		if strings.Contains(body, "$ref") || strings.Contains(body, "$defs") {
			t.Fatalf("%s: schema contains $ref/$defs (provider-incompatible): %s", tool.Name, body)
		}
	}

	// apply_restrictions must carry the restriction + severity enums.
	_, _, arMap := ApplyRestrictions(nil).Schema()
	props := arMap["properties"].(map[string]any)
	restrProps := props["restrictions"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	restrEnum := restrProps["restriction"].(map[string]any)["enum"].([]any)
	if len(restrEnum) != len(ALL_RESTRICTIONS) {
		t.Fatalf("expected %d restriction enum entries, got %d", len(ALL_RESTRICTIONS), len(restrEnum))
	}
	sevEnum := restrProps["severity"].(map[string]any)["enum"].([]any)
	if len(sevEnum) != 3 {
		t.Fatalf("expected 3 severity enum entries, got %d", len(sevEnum))
	}

	// generate_recipe must carry the nested ingredient shape.
	_, _, gr := GenerateRecipe().Schema()
	grProps := gr["properties"].(map[string]any)
	ingProps := grProps["ingredients"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if _, ok := ingProps["amount"]; !ok {
		t.Fatalf("generate_recipe schema missing ingredient.amount")
	}
}
