package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/orionryan/diet-app-go/internal/ai/provider"
	"github.com/orionryan/diet-app-go/internal/auth"
)

// scanIngredient is one extracted ingredient plus its blacklist match (if any).
type scanIngredient struct {
	Name    string
	Match   string // blacklist entry this ingredient matched; empty = safe
	Conflict bool
}

// ScanPage renders the ingredient-scanner UI.
func (h *Handlers) ScanPage(c echo.Context) error {
	return h.page(c, "scan", nil)
}

// ScanIngredients accepts one or more images (multipart "image"/"images"),
// asks the configured vision model to extract every visible ingredient as
// JSON, then cross-checks the list against the user's blacklist server-side
// (deterministic substring match — we don't trust the model to do matching).
func (h *Handlers) ScanIngredients(c echo.Context) error {
	u := auth.CurrentUser(c)
	if u == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "sign in first")
	}

	rl := h.Limiter.Allow("scan:" + u.ID)
	if !rl.Allowed {
		return h.renderScanError(c, "You're going a bit fast — try again in a few seconds.")
	}

	// ---- collect uploaded images ----
	tmpDir, err := os.MkdirTemp("", "dietapp-scan-*")
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	defer os.RemoveAll(tmpDir)

	var imagePaths []string
	form, ferr := c.MultipartForm()
	if ferr == nil && form != nil {
		for _, key := range []string{"image", "images"} {
			for _, fh := range form.File[key] {
				if fh.Size > 25*1024*1024 {
					continue
				}
				src, err := fh.Open()
				if err != nil {
					continue
				}
				dst, err := os.Create(filepath.Join(tmpDir, filepath.Base(fh.Filename)))
				if err != nil {
					src.Close()
					continue
				}
				if _, err := io.Copy(dst, src); err != nil {
					src.Close()
					dst.Close()
					continue
				}
				src.Close()
				dst.Close()
				imagePaths = append(imagePaths, dst.Name())
			}
		}
	}
	if len(imagePaths) == 0 {
		return h.renderScanError(c, "No image received. Take a photo, upload a file, or paste a screenshot.")
	}

	ctx := c.Request().Context()

	// ---- load user context ----
	settings, err := h.loadSettings(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	blacklist, err := h.loadBlacklist(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	blNames := make([]string, 0, len(blacklist))
	for _, b := range blacklist {
		blNames = append(blNames, b.Ingredient)
	}

	src, model, apiKey, baseURL := h.resolveProvider(settings)

	const scanPrompt = `List every ingredient you can identify in this image. If it shows a food label, read the full ingredients list. If it shows a meal or product photo, identify the visible ingredients.
Return ONLY a JSON array of ingredient names as lowercase strings, e.g. ["wheat flour","sugar","palm oil"]. No prose, no markdown, no explanation.`

	// ---- call vision ----
	var reply string
	if src == provider.SourceLocal {
		if h.LocalAI == nil || !h.LocalAI.IsVision() {
			return h.renderScanError(c, "No local vision model loaded. Visit /models to load a vision-capable model, or switch to a vision-capable cloud model on /profile.")
		}
		resp, vErr := h.LocalAI.ChatVision(ctx, provider.ChatRequest{Messages: []provider.Message{
			{Role: "system", Content: "You are an ingredient-extraction engine. You output only JSON."},
			{Role: "user", Content: scanPrompt},
		}}, imagePaths)
		if vErr != nil {
			return h.renderScanError(c, "The AI call failed: "+vErr.Error())
		}
		if len(resp.Choices) > 0 {
			reply = resp.Choices[0].Message.Content
		}
	} else {
		client := provider.New(provider.Options{
			Source:  src,
			Model:   model,
			APIKey:  apiKey,
			BaseURL: baseURL,
			AppURL:  h.Cfg.OpenRouterAppURL,
			AppName: h.Cfg.OpenRouterAppName,
		})
		images := make([]provider.ImageInput, 0, len(imagePaths))
		for _, p := range imagePaths {
			data, rErr := os.ReadFile(p)
			if rErr != nil {
				continue
			}
			mime := http.DetectContentType(data)
			if !strings.HasPrefix(mime, "image/") {
				mime = "image/jpeg"
			}
			images = append(images, provider.ImageInput{Data: data, MimeType: mime})
		}
		resp, vErr := client.ChatVision(ctx, model,
			"You are an ingredient-extraction engine. You output only JSON.",
			scanPrompt, images)
		if vErr != nil {
			return h.renderScanError(c, "The AI call failed: "+vErr.Error())
		}
		if resp.Error != nil {
			return h.renderScanError(c, "The AI provider returned an error: "+resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			reply = resp.Choices[0].Message.Content
		}
	}

	if reply == "" {
		return h.renderScanError(c, "The model returned no reply. Try a clearer photo.")
	}

	// ---- parse the JSON ingredient list ----
	ingredients := parseIngredientJSON(reply)
	if len(ingredients) == 0 {
		return h.renderScanError(c, "Couldn't extract any ingredients from that image. Try a sharper, well-lit photo of the label.")
	}

	// ---- match against blacklist ----
	results := make([]scanIngredient, 0, len(ingredients))
	conflicts := 0
	for _, ing := range ingredients {
		match := matchBlacklistEntry(ing, blNames)
		si := scanIngredient{Name: ing, Match: match, Conflict: match != ""}
		if si.Conflict {
			conflicts++
		}
		results = append(results, si)
	}

	return h.renderScanResults(c, results, conflicts)
}

// parseIngredientJSON extracts a JSON string array from model output. Models
// often wrap JSON in markdown fences or add prose, so we slice from the
// first '[' to the last ']'. Falls back to comma/newline splitting.
func parseIngredientJSON(reply string) []string {
	start := strings.Index(reply, "[")
	end := strings.LastIndex(reply, "]")
	if start >= 0 && end > start {
		var out []string
		if err := json.Unmarshal([]byte(reply[start:end+1]), &out); err == nil {
			return cleanIngredients(out)
		}
	}
	// Fallback: split on newlines/commas, strip bullets and quotes.
	replacer := strings.NewReplacer("\"", "", "- ", "", "* ", "", "`", "")
	reply = replacer.Replace(reply)
	var out []string
	for _, part := range strings.FieldsFunc(reply, func(r rune) bool { return r == '\n' || r == ',' }) {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return cleanIngredients(out)
}

func cleanIngredients(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		s = strings.Trim(s, ".,;")
		if s == "" || len(s) > 80 || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// matchBlacklistEntry returns the blacklist entry that matches the given
// ingredient (case-insensitive, bidirectional substring so "peanut" matches
// "peanut oil" and "roasted peanuts"), or "" if no conflict.
func matchBlacklistEntry(ingredient string, blacklist []string) string {
	norm := strings.ToLower(strings.TrimSpace(ingredient))
	if norm == "" {
		return ""
	}
	normStem := stemToken(norm)
	for _, b := range blacklist {
		bn := strings.ToLower(strings.TrimSpace(b))
		if bn == "" {
			continue
		}
		if norm == bn || strings.Contains(norm, bn) || strings.Contains(bn, norm) {
			return b
		}
		// Stemmed comparison handles plural/singular: "peanut oil" should
		// match blacklist "peanuts" even though it lacks the literal "peanuts"
		// substring.
		bStem := stemToken(bn)
		if bStem != "" && (strings.Contains(normStem, bStem) || strings.Contains(bStem, normStem)) {
			return b
		}
	}
	return ""
}

// stemToken applies crude plural stripping (trailing "s"/"es") to every
// whitespace-separated token. Used only for comparison, never displayed.
func stemToken(s string) string {
	toks := strings.Fields(s)
	for i, t := range toks {
		if len(t) > 3 {
			toks[i] = strings.TrimSuffix(strings.TrimSuffix(t, "es"), "s")
		}
	}
	return strings.Join(toks, " ")
}

func (h *Handlers) renderScanResults(c echo.Context, ingredients []scanIngredient, conflicts int) error {
	c.Response().Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.Templates.RenderPartial(c.Response().Writer, "partials/scan_results.html", map[string]any{
		"Ingredients": ingredients,
		"Conflicts":   conflicts,
	})
}

func (h *Handlers) renderScanError(c echo.Context, msg string) error {
	c.Response().Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.Templates.RenderPartial(c.Response().Writer, "partials/scan_results.html", map[string]any{
		"Error": msg,
	})
}
