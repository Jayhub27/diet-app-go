package tools

import (
	"strings"
	"testing"
)

func TestParseMCPTextJSONAndSSE(t *testing.T) {
	jsonBody := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"hello"}]}}`
	if got := parseMCPText(jsonBody); got != "hello" {
		t.Fatalf("JSON parse = %q", got)
	}
	sse := "event: message\ndata: " + jsonBody + "\n\n"
	if got := parseMCPText(sse); got != "hello" {
		t.Fatalf("SSE parse = %q", got)
	}
	if got := parseMCPText("<html>blocked</html>"); got != "" {
		t.Fatalf("malformed body = %q", got)
	}
}

func TestParseExaText(t *testing.T) {
	text := `Title: A Buttermilk Substitute for Every Scenario
URL: https://example.com/buttermilk
Published: 2023-09-27
Highlights:
Kefir or thinned yogurt works best.

---

Title: 6 best buttermilk substitutes
URL: https://example.com/six
Highlights:
Milk and lemon juice work for baking.`
	results := parseExaText(text)
	if len(results) != 2 {
		t.Fatalf("got %d results", len(results))
	}
	if results[0].Title != "A Buttermilk Substitute for Every Scenario" ||
		results[0].URL != "https://example.com/buttermilk" ||
		!strings.Contains(results[0].Snippet, "Kefir") {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
}

func TestParseParallelText(t *testing.T) {
	text := `{"results":[{"url":"https://example.com/a","title":"A","excerpts":["Milk and lemon juice."]},{"title":"no url"}]}`
	results := parseParallelText(text)
	if len(results) != 1 || results[0].URL != "https://example.com/a" ||
		!strings.Contains(results[0].Snippet, "Milk") {
		t.Fatalf("unexpected results: %+v", results)
	}
}
