package handlers

import (
	"strings"
	"testing"
)

func TestParseIngredientLine(t *testing.T) {
	cases := []struct {
		line   string
		name   string
		amount float64
		unit   string
	}{
		{"1 ½ cups all-purpose flour, sifted", "all-purpose flour", 1.5, "cup"},
		{"¾ cup sugar", "sugar", 0.75, "cup"},
		{"1/2 teaspoon salt", "salt", 0.5, "tsp"},
		{"1-2 tbsp olive oil", "olive oil", 1, "tbsp"},
		{"2 cups of milk", "milk", 2, "cup"},
		{"1 (14 oz) can diced tomatoes", "diced tomatoes", 1, "can"},
		{"1 garlic clove, finely grated", "garlic", 1, "clove"},
		{"1 small bunch parsley, finely chopped", "parsley", 1, "bunch"},
		{"250 g button chestnut mushroom sliced", "button chestnut mushroom", 250, "g"},
		{"3 large eggs", "large eggs", 3, ""},
		{"Salt and pepper to taste", "Salt and pepper to taste", 1, ""},
	}
	for _, tc := range cases {
		got, ok := parseIngredientLine(tc.line)
		if !ok {
			t.Fatalf("parseIngredientLine(%q) failed", tc.line)
		}
		if got.Name != tc.name || got.Amount != tc.amount || got.Unit != tc.unit {
			t.Errorf("parseIngredientLine(%q) = {%q, %v, %q}, want {%q, %v, %q}",
				tc.line, got.Name, got.Amount, got.Unit, tc.name, tc.amount, tc.unit)
		}
	}
}

func TestParseRecipeHTML(t *testing.T) {
	page := `<!doctype html><html><head>
	<script type="application/ld+json">
	{
	  "@context": "https://schema.org",
	  "@type": "Recipe",
	  "name": "Lemon Herb Chicken",
	  "description": "A bright weeknight dinner.",
	  "recipeYield": "4 servings",
	  "prepTime": "PT10M",
	  "cookTime": "PT25M",
	  "recipeIngredient": ["1 ½ lbs chicken thighs", "2 tbsp olive oil", "Salt and pepper to taste"],
	  "recipeInstructions": [
	    {"@type": "HowToStep", "text": "Heat the oven."},
	    {"@type": "HowToSection", "itemListElement": [
	      {"@type": "HowToStep", "text": "Roast the chicken."}
	    ]}
	  ],
	  "suitableForDiet": ["https://schema.org/GlutenFreeDiet"],
	  "recipeCategory": "Dinner"
	}
	</script></head><body></body></html>`

	rec, err := parseRecipeHTML(page, "https://example.com/chicken")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Title != "Lemon Herb Chicken" || rec.Servings != 4 || rec.PrepMinutes != 10 || rec.CookMinutes != 25 {
		t.Fatalf("unexpected recipe: %+v", rec)
	}
	if len(rec.Ingredients) != 3 || rec.Ingredients[0].Unit != "lb" {
		t.Fatalf("unexpected ingredients: %+v", rec.Ingredients)
	}
	if len(rec.Steps) != 2 || rec.Steps[1] != "Roast the chicken." {
		t.Fatalf("unexpected steps: %+v", rec.Steps)
	}
	if strings.Join(rec.DietaryTags, ",") != "gluten-free,dinner" {
		t.Fatalf("unexpected tags: %+v", rec.DietaryTags)
	}
}

func TestParseRecipeHTMLWithoutRecipe(t *testing.T) {
	if _, err := parseRecipeHTML("<html><body>hello</body></html>", "https://example.com"); err == nil {
		t.Fatal("expected an error for a page without recipe data")
	}
}

func TestDurationMinutes(t *testing.T) {
	cases := map[string]int{
		"PT1H30M":     90,
		"PT20M":       20,
		"P1DT2H":      1560,
		"1 hr 20 min": 80,
		"45 minutes":  45,
	}
	for input, want := range cases {
		if got := durationMinutes(input); got != want {
			t.Errorf("durationMinutes(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestValidateImportURLRejectsPrivateHosts(t *testing.T) {
	for _, raw := range []string{
		"http://localhost/recipe",
		"http://127.0.0.1/recipe",
		"http://10.0.0.1/recipe",
		"http://169.254.169.254/latest/meta-data/",
		"file:///etc/passwd",
	} {
		if _, err := validateImportURL(raw); err == nil {
			t.Errorf("validateImportURL(%q) accepted a blocked URL", raw)
		}
	}
}
