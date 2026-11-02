package templates

import (
	"bytes"
	"strings"
	"testing"

	"github.com/orionryan/diet-app-go/internal/ai/tools"
)

func renderPage(t *testing.T, name string, data map[string]any) string {
	t.Helper()
	engine, err := New()
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{
		"Title":    name,
		"User":     nil,
		"DarkMode": false,
	}
	for k, v := range data {
		base[k] = v
	}
	var buf bytes.Buffer
	if err := engine.Render(&buf, name, base); err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return buf.String()
}

func TestRenderGroceriesPage(t *testing.T) {
	html := renderPage(t, "groceries", map[string]any{
		"Groups": []tools.GroceryCategory{{
			Category: "Produce",
			Items: []tools.GroceryItem{{
				Name: "spinach", Amount: 1, Unit: "bag", Checked: false,
				FromRecipes: []string{"Added by you"},
			}},
		}},
		"Total": 1, "Checked": 0, "Remaining": 1, "WeekStart": "2026-11-02",
	})
	for _, want := range []string{
		"grocery-panel", "spinach", "1 bag", "Added by you",
		"1 to buy", "grocery-clear-dialog", "Mark spinach as bought",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("groceries page missing %q", want)
		}
	}
}

func TestRenderProfilePageWithCalorieTarget(t *testing.T) {
	html := renderPage(t, "profile", map[string]any{
		"Restrictions": []map[string]any{{
			"ID": "r1", "Restriction": "vegan", "Severity": "avoid",
		}},
		"Blacklist": []map[string]any{{
			"ID": "b1", "Ingredient": "peanut butter",
		}},
		"Settings":      map[string]any{"Source": "openrouter", "ModelID": "test-model"},
		"CalorieTarget": 1850,
	})
	for _, want := range []string{
		"value=\"1850\"", "Current target: 1850 kcal/day", "peanut butter", "vegan",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("profile page missing %q", want)
		}
	}
}
