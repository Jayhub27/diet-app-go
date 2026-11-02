package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Web search uses the same hosted MCP crawlers as the Next.js app and
// OpenCode: Exa first, then Parallel. Both work without a key for light use;
// EXA_API_KEY / PARALLEL_API_KEY raise the limits.

const mcpSearchTimeout = 12 * time.Second

type webSearchArgs struct {
	Query      string `json:"query" jsonschema_description:"What to search for, e.g. 'November seasonal produce' or 'best gluten-free soy sauce brand'"`
	MaxResults int    `json:"maxResults,omitempty" jsonschema:"minimum=1,maximum=8" jsonschema_description:"Number of results to return (default 5)"`
}

type mcpSearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// SearchWeb registers the web_search tool.
func SearchWeb() *Tool {
	return &Tool{
		Name: "web_search",
		Description: "Search the web for current real-world information: ingredient seasonality, " +
			"brands or stores, product substitutions, or facts that aren't in the local tables. " +
			"Returns titles, URLs and snippets. For packaged grocery products use find_products instead — " +
			"it returns the ingredient list and a diet-fit verdict. The current year is 2026. " +
			"If it returns no results, do not retry — answer from your own knowledge and say the web search was unavailable.",
		Parameters: schemaFor(&webSearchArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in webSearchArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			query := strings.TrimSpace(in.Query)
			if query == "" {
				return "", fmt.Errorf("query is required")
			}
			max := in.MaxResults
			if max <= 0 {
				max = 5
			}
			if max > 8 {
				max = 8
			}
			results := searchExa(ctx, query, max)
			if len(results) == 0 {
				results = searchParallel(ctx, query, max)
			}
			if len(results) == 0 {
				return "Web search returned no results. The search providers may be blocked or rate-limited; answer from your own knowledge and don't retry.", nil
			}
			var b strings.Builder
			for i, result := range results {
				if i >= max {
					break
				}
				fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, result.Title, result.URL)
				if result.Snippet != "" {
					fmt.Fprintf(&b, "   %s\n", result.Snippet)
				}
			}
			return b.String(), nil
		},
	}
}

func exaMCPURL() string {
	key := strings.TrimSpace(os.Getenv("EXA_API_KEY"))
	if key == "" {
		return "https://mcp.exa.ai/mcp"
	}
	return "https://mcp.exa.ai/mcp?exaApiKey=" + url.QueryEscape(key)
}

func searchExa(ctx context.Context, query string, max int) []mcpSearchResult {
	text, err := callMCP(ctx, exaMCPURL(), "web_search_exa", map[string]any{
		"query": query, "type": "auto", "numResults": max,
		"livecrawl": "fallback", "contextMaxCharacters": 10000,
	}, nil)
	if err != nil || text == "" {
		return nil
	}
	return parseExaText(text)
}

func searchParallel(ctx context.Context, query string, max int) []mcpSearchResult {
	headers := map[string]string{}
	if key := strings.TrimSpace(os.Getenv("PARALLEL_API_KEY")); key != "" {
		headers["authorization"] = "Bearer " + key
	}
	text, err := callMCP(ctx, "https://search.parallel.ai/mcp", "web_search", map[string]any{
		"objective": query, "search_queries": []string{query},
	}, headers)
	if err != nil || text == "" {
		return nil
	}
	results := parseParallelText(text)
	if len(results) > max {
		results = results[:max]
	}
	return results
}

func callMCP(ctx context.Context, endpoint, toolName string, args map[string]any, headers map[string]string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	req.Header.Set("user-agent", "DietApp/1.0 (+https://github.com/Jayhub27/Diet-App)")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: mcpSearchTimeout}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("search provider returned %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return "", err
	}
	return parseMCPText(string(raw)), nil
}

// parseMCPText extracts result.content[].text from a JSON or SSE MCP response.
func parseMCPText(raw string) string {
	extract := func(value any) string {
		obj, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		result, ok := obj["result"].(map[string]any)
		if !ok {
			return ""
		}
		content, ok := result["content"].([]any)
		if !ok {
			return ""
		}
		for _, part := range content {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if p["type"] == "text" {
				if text, ok := p["text"].(string); ok {
					return text
				}
			}
		}
		return ""
	}

	var value any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &value); err == nil {
		if text := extract(value); text != "" {
			return text
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &value); err == nil {
			if text := extract(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func parseExaText(text string) []mcpSearchResult {
	var out []mcpSearchResult
	for _, block := range strings.Split(text, "\n---") {
		block = strings.TrimSpace(strings.TrimPrefix(block, "---"))
		lines := strings.Split(block, "\n")
		var result mcpSearchResult
		var snippet []string
		inHighlights := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(trimmed, "Title:"):
				result.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "Title:"))
			case strings.HasPrefix(trimmed, "URL:"):
				result.URL = strings.TrimSpace(strings.TrimPrefix(trimmed, "URL:"))
			case trimmed == "Highlights:":
				inHighlights = true
			case inHighlights:
				snippet = append(snippet, trimmed)
			}
		}
		if result.Title != "" && result.URL != "" {
			result.Snippet = truncateSnippet(strings.Join(snippet, " "))
			out = append(out, result)
		}
	}
	return out
}

func parseParallelText(text string) []mcpSearchResult {
	var payload struct {
		Results []struct {
			URL      string   `json:"url"`
			Title    string   `json:"title"`
			Excerpts []string `json:"excerpts"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil
	}
	var out []mcpSearchResult
	for _, r := range payload.Results {
		if r.URL == "" {
			continue
		}
		title := r.Title
		if title == "" {
			title = r.URL
		}
		snippet := ""
		if len(r.Excerpts) > 0 {
			snippet = r.Excerpts[0]
		}
		out = append(out, mcpSearchResult{Title: title, URL: r.URL, Snippet: truncateSnippet(snippet)})
	}
	return out
}

func truncateSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 300 {
		return string(r[:300])
	}
	return s
}
