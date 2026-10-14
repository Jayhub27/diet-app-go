package templates

import (
	"bytes"
	"strings"
	"testing"
)

// Renders both branches of the scan results partial: a barcode-matched
// product card and the label-read fallback with blacklist matches.
func TestScanPartialRenders(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	product := map[string]any{
		"Name": "Nutella", "Brand": "Ferrero", "ImageURL": "", "URL": "https://x",
		"Verdict": "unsafe", "VerdictText": "Conflicts with your restrictions.",
		"Violations":  []map[string]any{{"Detail": "contains nuts", "Severity": "anaphylaxis"}},
		"Notes":       []string{"labeled gluten-free"},
		"Ingredients": "Sugar, palm oil", "Allergens": "milk, nuts", "Traces": "soy", "Stores": "Tesco",
	}
	var buf bytes.Buffer
	if err := e.RenderPartial(&buf, "partials/scan_results.html", map[string]any{
		"Product": product, "Ingredients": []any{}, "Conflicts": 0, "BarcodeNote": "",
	}); err != nil {
		t.Fatalf("render product: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Nutella", "contains nuts", "Sugar, palm oil", "Open Food Facts"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}

	buf.Reset()
	if err := e.RenderPartial(&buf, "partials/scan_results.html", map[string]any{
		"Product": nil,
		"Ingredients": []map[string]any{
			{"Name": "wheat flour", "Match": "wheat", "Conflict": true},
			{"Name": "sugar", "Match": "", "Conflict": false},
		},
		"Conflicts": 1, "BarcodeNote": "Barcode 123 isn't in Open Food Facts.",
	}); err != nil {
		t.Fatalf("render fallback: %v", err)
	}
	out = buf.String()
	for _, want := range []string{"wheat flour", "matches blacklist", "Barcode 123", "in Open Food Facts"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
}
