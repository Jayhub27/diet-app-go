package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withMockOFF points the Open Food Facts base URLs at a test server for the
// duration of the test.
func withMockOFF(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	oldSearch, oldProduct := offSearchURL, offProductURL
	offSearchURL = srv.URL + "/search"
	offProductURL = srv.URL + "/product"
	t.Cleanup(func() {
		offSearchURL, offProductURL = oldSearch, oldProduct
		srv.Close()
	})
}

func TestNormalizeBarcode(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"012345678905", "0012345678905", true},   // UPC-A zero-padded
		{"00012345678905", "0012345678905", true}, // GTIN-14 leading zero trimmed
		{"4 006381 333931", "4006381333931", true},
		{"4006381333931", "4006381333931", true},
		{"96385074", "96385074", true},
		{"12345", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeBarcode(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("normalizeBarcode(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestIsValidBarcode(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"4006381333931", true},
		{"0012345678905", true},
		{"96385074", true},
		{"4006381333932", false},
		{"12345", false},
	}
	for _, c := range cases {
		if got := isValidBarcode(c.in); got != c.want {
			t.Errorf("isValidBarcode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMatchAllergenTag(t *testing.T) {
	milk := matchAllergenTag("en:milk")
	restrictions := map[string]bool{}
	for _, r := range milk {
		for _, name := range r.restrictions {
			restrictions[name] = true
		}
	}
	if !restrictions["dairy-free"] || !restrictions["vegan"] {
		t.Fatalf("milk tag should violate dairy-free and vegan, got %v", restrictions)
	}
	if got := matchAllergenTag("en:cultured-nonfat-milk"); len(got) != 1 {
		t.Fatalf("verbose milk tag should match one rule, got %d", len(got))
	}
	if got := matchAllergenTag("en:tree-nuts"); len(got) == 0 || got[0].restrictions[0] != "nut-free" {
		t.Fatalf("tree-nuts should map to nut-free, got %v", got)
	}
	if got := matchAllergenTag("en:coconut-oil"); len(got) != 0 {
		t.Fatalf("coconut is not a tree nut, got %v", got)
	}
}

func TestSplitIngredientText(t *testing.T) {
	got := splitIngredientText("Wheat flour 13%, water, salt (2%), sugar")
	want := []string{"Wheat flour", "water", "salt", "sugar"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("split = %v, want %v", got, want)
	}

	got = splitIngredientText("Ingredients: Cereals (maize flour, rice), Contains: milk. May contain: nuts")
	want = []string{"maize flour", "rice", "milk", "nuts"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("split with labels = %v, want %v", got, want)
	}
}

func product(overrides func(p *ProductCandidate)) ProductCandidate {
	p := ProductCandidate{
		Code: "123",
		Name: "Test product",
		URL:  "https://world.openfoodfacts.org/product/123",
	}
	if overrides != nil {
		overrides(&p)
	}
	return p
}

func TestEvaluateProduct(t *testing.T) {
	glutenFree := []Restriction{{Name: "gluten-free", Severity: SeverityAvoid}}
	dairyFree := []Restriction{{Name: "dairy-free", Severity: SeverityAvoid}}
	vegan := []Restriction{{Name: "vegan", Severity: SeverityAvoid}}

	t.Run("flags an allergen tag as unsafe", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.Allergens = []string{"en:gluten"}
			p.IngredientsText = "Oats, water"
			p.IngredientsLanguage = "en"
		})
		out := evaluateProductJSON(p, glutenFree, nil)
		if out.Verdict != "unsafe" || len(out.Violations) == 0 || out.Violations[0].Kind != "allergen" {
			t.Fatalf("want unsafe allergen violation, got %+v", out)
		}
	})

	t.Run("refuses may-contain traces at anaphylaxis severity", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.Traces = []string{"en:milk"}
			p.IngredientsText = "Water, oats"
			p.IngredientsLanguage = "en"
		})
		out := evaluateProductJSON(p, []Restriction{{Name: "dairy-free", Severity: SeverityAnaphylaxis}}, nil)
		if out.Verdict != "unsafe" || out.Violations[0].Kind != "trace" {
			t.Fatalf("want unsafe trace violation, got %+v", out)
		}
	})

	t.Run("downgrades traces to check for lower severities", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.Traces = []string{"en:milk"}
			p.IngredientsText = "Water, oats"
			p.IngredientsLanguage = "en"
		})
		out := evaluateProductJSON(p, dairyFree, nil)
		if out.Verdict != "check" || len(out.Violations) != 0 {
			t.Fatalf("want check without violations, got %+v", out)
		}
	})

	t.Run("finds keyword violations in the ingredient list", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.IngredientsText = "Water, honey, lemon juice"
			p.IngredientsLanguage = "en"
		})
		out := evaluateProductJSON(p, vegan, nil)
		if out.Verdict != "unsafe" || out.Violations[0].Kind != "ingredient" {
			t.Fatalf("want unsafe ingredient violation, got %+v", out)
		}
	})

	t.Run("honours the safety exceptions (peanut butter is vegan)", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.IngredientsText = "Peanut butter, oats"
			p.IngredientsLanguage = "en"
		})
		out := evaluateProductJSON(p, vegan, nil)
		if out.Verdict != "safe" || len(out.Violations) != 0 {
			t.Fatalf("want safe, got %+v", out)
		}
	})

	t.Run("does not flag plant milks as dairy", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.IngredientsText = "Cultured pasteurized flaxmilk, coconutmilk, pectin"
			p.IngredientsLanguage = "en"
		})
		out := evaluateProductJSON(p, append(dairyFree, vegan...), nil)
		if out.Verdict != "safe" {
			t.Fatalf("want safe, got %+v", out)
		}
	})

	t.Run("still flags real dairy compounds (buttermilk)", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.IngredientsText = "Buttermilk, salt"
			p.IngredientsLanguage = "en"
		})
		out := evaluateProductJSON(p, dairyFree, nil)
		if out.Verdict != "unsafe" {
			t.Fatalf("want unsafe, got %+v", out)
		}
	})

	t.Run("does not match keywords inside longer words", func(t *testing.T) {
		for _, text := range []string{"L. RHAMNOSUS, pectin", "graham crackers"} {
			p := product(func(p *ProductCandidate) {
				p.IngredientsText = text
				p.IngredientsLanguage = "en"
			})
			out := evaluateProductJSON(p, vegan, nil)
			if out.Verdict != "safe" {
				t.Fatalf("%q: want safe, got %+v", text, out)
			}
		}
	})

	t.Run("treats coconut meat and butter beans as plant foods", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.IngredientsText = "Coconut meat, water"
			p.IngredientsLanguage = "en"
		})
		out := evaluateProductJSON(p, []Restriction{{Name: "vegan", Severity: SeverityAvoid}, {Name: "vegetarian", Severity: SeverityAvoid}}, nil)
		if out.Verdict != "safe" {
			t.Fatalf("coconut meat: want safe, got %+v", out)
		}
		p = product(func(p *ProductCandidate) {
			p.IngredientsText = "Butter beans, water"
			p.IngredientsLanguage = "en"
		})
		if out = evaluateProductJSON(p, vegan, nil); out.Verdict != "safe" {
			t.Fatalf("butter beans: want safe, got %+v", out)
		}
	})

	t.Run("treats maltodextrin as gluten-free", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.IngredientsText = "Maltodextrin, corn starch"
			p.IngredientsLanguage = "en"
		})
		if out := evaluateProductJSON(p, glutenFree, nil); out.Verdict != "safe" {
			t.Fatalf("want safe, got %+v", out)
		}
	})

	t.Run("reports blacklist hits", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.IngredientsText = "Rice, cilantro"
			p.IngredientsLanguage = "en"
		})
		out := evaluateProductJSON(p, nil, []string{"cilantro"})
		if out.Verdict != "unsafe" || out.Violations[0].Kind != "blacklist" {
			t.Fatalf("want unsafe blacklist violation, got %+v", out)
		}
	})

	t.Run("notes positive labels", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.Labels = []string{"en:no-gluten"}
			p.IngredientsText = "Corn flour, water"
			p.IngredientsLanguage = "en"
		})
		out := evaluateProductJSON(p, glutenFree, nil)
		if out.Verdict != "safe" {
			t.Fatalf("want safe, got %+v", out)
		}
		found := false
		for _, n := range out.Notes {
			if n == "labeled gluten-free" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing label note: %v", out.Notes)
		}
	})

	t.Run("asks for a label check without data", func(t *testing.T) {
		out := evaluateProductJSON(product(nil), glutenFree, nil)
		if out.Verdict != "check" {
			t.Fatalf("want check, got %+v", out)
		}
	})

	t.Run("never says safe from a non-English ingredient list", func(t *testing.T) {
		p := product(func(p *ProductCandidate) {
			p.IngredientsText = "Sucre, lait"
			p.IngredientsLanguage = "fr"
		})
		if out := evaluateProductJSON(p, dairyFree, nil); out.Verdict != "check" {
			t.Fatalf("want check, got %+v", out)
		}
	})
}

func TestFindProductsTool(t *testing.T) {
	withMockOFF(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"hits": []map[string]any{
					{
						"code":           "1",
						"product_name":   "Oat bread",
						"brands":         []string{"Oatly"},
						"allergens_tags": []string{"en:gluten"},
						"labels_tags":    []string{},
					},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/product/1.json"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": 1,
				"product": map[string]any{
					"code":                "1",
					"product_name":        "Oat bread",
					"brands":              "Oatly",
					"ingredients_text":    "Oats, water",
					"ingredients_text_en": "Oats, water",
					"lang":                "en",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out := mustToolJSON(t, FindProducts([]Restriction{{Name: "gluten-free", Severity: SeverityAvoid}}, nil),
		`{"query":"oat bread"}`)
	if out["fallback"] != false {
		t.Fatalf("fallback should be false: %v", out)
	}
	products, ok := out["products"].([]any)
	if !ok || len(products) != 1 {
		t.Fatalf("expected one product: %v", out)
	}
	first := products[0].(map[string]any)
	if first["verdict"] != "unsafe" {
		t.Fatalf("want unsafe, got %v", first["verdict"])
	}
	violations := first["violations"].([]any)
	if len(violations) == 0 || violations[0].(map[string]any)["kind"] != "allergen" {
		t.Fatalf("want allergen violation, got %v", violations)
	}
}

func TestFindProductsFallback(t *testing.T) {
	withMockOFF(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	out := mustToolJSON(t, FindProducts(nil, nil), `{"query":"anything"}`)
	if out["fallback"] != true {
		t.Fatalf("want fallback true, got %v", out)
	}
}

func TestLookupProductByBarcodeTool(t *testing.T) {
	withMockOFF(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/product/0012345678905.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": 1,
				"product": map[string]any{
					"code":                "0012345678905",
					"product_name":        "Test yogurt",
					"brands":              "Test Brand",
					"ingredients_text":    "Cultured milk, sugar",
					"ingredients_text_en": "Cultured milk, sugar",
					"lang":                "en",
					"allergens_tags":      []string{"en:milk"},
				},
			})
		case "/product/4006381333931.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 0})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	dairyFree := []Restriction{{Name: "dairy-free", Severity: SeverityAvoid}}

	out := mustToolJSON(t, LookupProductByBarcode(dairyFree, nil), `{"barcode":"012345678905"}`)
	if out["status"] != "found" || out["barcode"] != "0012345678905" {
		t.Fatalf("want found 0012345678905, got %v", out)
	}
	productMap := out["product"].(map[string]any)
	if productMap["verdict"] != "unsafe" {
		t.Fatalf("want unsafe, got %v", productMap["verdict"])
	}

	out = mustToolJSON(t, LookupProductByBarcode(dairyFree, nil), `{"barcode":"4006381333931"}`)
	if out["status"] != "not_found" {
		t.Fatalf("want not_found, got %v", out)
	}

	out = mustToolJSON(t, LookupProductByBarcode(dairyFree, nil), `{"barcode":"4006381333932"}`)
	if out["status"] != "invalid_barcode" {
		t.Fatalf("want invalid_barcode, got %v", out)
	}
}

func TestLookupProductByBarcodeUnavailable(t *testing.T) {
	withMockOFF(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	out := mustToolJSON(t, LookupProductByBarcode(nil, nil), `{"barcode":"4006381333931"}`)
	if out["status"] != "source_unavailable" {
		t.Fatalf("want source_unavailable, got %v", out)
	}
}

// Sanity: the tool must be runnable through the generic Tool.Run path.
func TestProductToolsRun(t *testing.T) {
	withMockOFF(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search") {
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": []map[string]any{}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	for _, tool := range []*Tool{FindProducts(nil, nil), LookupProductByBarcode(nil, nil)} {
		if _, err := tool.Run(context.Background(), json.RawMessage(`{"query":"x","barcode":"4006381333931"}`)); err != nil {
			t.Fatalf("%s Run failed: %v", tool.Name, err)
		}
	}
}
