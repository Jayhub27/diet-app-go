package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/orionryan/diet-app-go/internal/ai/provider"
	"github.com/orionryan/diet-app-go/internal/ai/tools"
	"github.com/orionryan/diet-app-go/internal/auth"
)

// scanIngredient is one extracted ingredient plus its blacklist match (if any).
type scanIngredient struct {
	Name     string
	Match    string // blacklist entry this ingredient matched; empty = safe
	Conflict bool
}

// scanProduct is a barcode-matched product rendered on the scan page.
type scanProduct struct {
	Name        string
	Brand       string
	ImageURL    string
	URL         string
	Verdict     string // safe | check | unsafe
	VerdictText string
	Violations  []scanProductViolation
	Notes       []string
	Ingredients string
	Allergens   string
	Traces      string
	Stores      string
}

type scanProductViolation struct {
	Detail   string
	Severity string
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
	imagePaths = saveUploadedImages(c, tmpDir)
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
	restrictionRows, err := h.loadRestrictions(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	rs := make([]tools.Restriction, 0, len(restrictionRows))
	for _, r := range restrictionRows {
		rs = append(rs, tools.Restriction{Name: r.Restriction, Severity: tools.Severity(r.Severity)})
	}

	const scanPrompt = `Read this image and return ONLY a JSON object with two fields:
- "barcode": the digits printed under a visible barcode (string, no spaces). If no barcode is visible, use "".
- "ingredients": every ingredient you can identify, as lowercase strings. If the image shows a food label, read the full ingredients list; if it shows a meal or product photo, identify the visible ingredients.

Example: {"barcode":"4006381333931","ingredients":["wheat flour","sugar","palm oil"]}
No prose, no markdown, no explanation.`

	// ---- call vision ----
	reply, err := h.visionExtract(ctx, settings, imagePaths,
		"You are an ingredient-extraction engine. You output only JSON.", scanPrompt)
	if err != nil {
		return h.renderScanError(c, err.Error())
	}
	if reply == "" {
		return h.renderScanError(c, "The model returned no reply. Try a clearer photo.")
	}

	// ---- parse the model output (barcode + ingredient list) ----
	barcode, ingredients := parseScanResult(reply)

	// ---- barcode path: look the product up and check it against the profile ----
	var product *scanProduct
	var barcodeNote string
	if barcode != "" {
		lookup := tools.LookupBarcodeProduct(barcode, rs, blNames)
		switch lookup.Status {
		case "found":
			product = toScanProduct(lookup.Product)
		case "not_found":
			barcodeNote = "Barcode " + lookup.Barcode + " isn't in Open Food Facts — showing the ingredients read from the photo instead."
		case "invalid_barcode":
			barcodeNote = "The barcode digits couldn't be validated (a digit may be misread) — showing the ingredients read from the photo instead."
		default:
			barcodeNote = "Product data is unreachable right now — showing the ingredients read from the photo instead."
		}
	}

	if product == nil && len(ingredients) == 0 {
		return h.renderScanError(c, "Couldn't extract any ingredients from that image. Try a sharper, well-lit photo of the label or barcode.")
	}

	// ---- match against blacklist (label-read path) ----
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

	return h.renderScanResults(c, results, conflicts, product, barcodeNote, false)
}

// parseScanResult extracts the barcode and ingredient list from model output.
// The current prompt asks for {"barcode": "...", "ingredients": [...]};
// older/array-shaped replies fall back to the plain ingredient parser.
func parseScanResult(reply string) (string, []string) {
	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start >= 0 && end > start {
		var out struct {
			Barcode     string   `json:"barcode"`
			Ingredients []string `json:"ingredients"`
		}
		if err := json.Unmarshal([]byte(reply[start:end+1]), &out); err == nil {
			return strings.TrimSpace(out.Barcode), cleanIngredients(out.Ingredients)
		}
	}
	return "", parseIngredientJSON(reply)
}

func toScanProduct(p *tools.EvaluatedProduct) *scanProduct {
	if p == nil {
		return nil
	}
	verdictText := map[string]string{
		"safe":   "Fits your restrictions — no conflicts found in the label data.",
		"check":  "Check the label before buying — some data couldn't be verified.",
		"unsafe": "Conflicts with your restrictions or blacklist. Avoid it.",
	}[p.Verdict]
	if verdictText == "" {
		verdictText = "Fit could not be determined — check the label."
	}
	violations := make([]scanProductViolation, 0, len(p.Violations))
	for _, v := range p.Violations {
		violations = append(violations, scanProductViolation{Detail: v.Detail, Severity: string(v.Severity)})
	}
	return &scanProduct{
		Name:        p.Name,
		Brand:       p.Brand,
		ImageURL:    p.ImageURL,
		URL:         p.URL,
		Verdict:     p.Verdict,
		VerdictText: verdictText,
		Violations:  violations,
		Notes:       p.Notes,
		Ingredients: p.IngredientsText,
		Allergens:   beautifyTags(p.Allergens),
		Traces:      beautifyTags(p.Traces),
		Stores:      p.Stores,
	}
}

// beautifyTags turns raw OFF tags ("en:no-gluten") into readable text
// ("no gluten") for the scan page.
func beautifyTags(tags []string) string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(t)
		if len(t) >= 3 && t[2] == ':' {
			t = t[3:]
		}
		out = append(out, strings.ReplaceAll(t, "-", " "))
	}
	return strings.Join(out, ", ")
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

func (h *Handlers) renderScanResults(c echo.Context, ingredients []scanIngredient, conflicts int, product *scanProduct, barcodeNote string, listMode bool) error {
	c.Response().Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.Templates.RenderPartial(c.Response().Writer, "partials/scan_results.html", map[string]any{
		"Ingredients": ingredients,
		"Conflicts":   conflicts,
		"Product":     product,
		"BarcodeNote": barcodeNote,
		"ListMode":    listMode,
	})
}

func (h *Handlers) renderScanError(c echo.Context, msg string) error {
	c.Response().Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.Templates.RenderPartial(c.Response().Writer, "partials/scan_results.html", map[string]any{
		"Error": msg,
	})
}

// ---- paste-a-list checking (no AI) ----

// ScanList checks a pasted ingredient list against the user's blacklist. It is
// deterministic, so it works without any vision model or API key.
func (h *Handlers) ScanList(c echo.Context) error {
	u := auth.CurrentUser(c)
	if u == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "sign in first")
	}
	rl := h.Limiter.Allow("scan:" + u.ID)
	if !rl.Allowed {
		return h.renderScanError(c, "You're going a bit fast — try again in a few seconds.")
	}

	raw := strings.TrimSpace(c.FormValue("ingredients"))
	if raw == "" {
		return h.renderScanError(c, "Paste an ingredient list first.")
	}
	items := parseIngredientList(raw)
	if len(items) == 0 {
		return h.renderScanError(c, "Couldn't find any ingredients in that text.")
	}

	blacklist, err := h.loadBlacklist(c.Request().Context(), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	blNames := make([]string, 0, len(blacklist))
	for _, b := range blacklist {
		blNames = append(blNames, b.Ingredient)
	}

	results := make([]scanIngredient, 0, len(items))
	conflicts := 0
	for _, item := range items {
		match := matchBlacklistEntry(item, blNames)
		results = append(results, scanIngredient{Name: item, Match: match, Conflict: match != ""})
		if match != "" {
			conflicts++
		}
	}
	return h.renderScanResults(c, results, conflicts, nil, "", true)
}

// parseIngredientList splits a pasted label into individual ingredients. It
// splits on newlines, semicolons and top-level commas, keeping commas inside
// parentheses (e.g. "milk (2% milkfat, vitamin D)") intact.
func parseIngredientList(raw string) []string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw == "" {
		return nil
	}
	if lower := strings.ToLower(raw); strings.HasPrefix(lower, "ingredients:") {
		raw = raw[len("ingredients:"):]
	}

	var parts []string
	var b strings.Builder
	depth := 0
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			parts = append(parts, s)
		}
		b.Reset()
	}
	for _, r := range raw {
		switch r {
		case '(', '[':
			depth++
			b.WriteRune(r)
		case ')', ']':
			if depth > 0 {
				depth--
			}
			b.WriteRune(r)
		case '\n', ';', '•':
			flush()
		case ',':
			if depth > 0 {
				b.WriteRune(r)
			} else {
				flush()
			}
		default:
			b.WriteRune(r)
		}
	}
	flush()

	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(strings.Trim(p, "-–—*·."))
		p = strings.TrimSpace(strings.TrimLeft(p, "0123456789.) "))
		p = strings.Join(strings.Fields(p), " ")
		if p == "" || len(p) > 120 {
			continue
		}
		key := strings.ToLower(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
		if len(out) >= 200 {
			break
		}
	}
	return out
}

// ---- scan an ingredient list into the blacklist ----

// BlacklistScan reads an ingredient list from a photo and adds every name to
// the user's blacklist. The response includes an OOB swap of #blacklist-list.
func (h *Handlers) BlacklistScan(c echo.Context) error {
	u := auth.CurrentUser(c)
	if u == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "sign in first")
	}
	rl := h.Limiter.Allow("scan:" + u.ID)
	if !rl.Allowed {
		return h.renderBlacklistScan(c, "You're going a bit fast — try again in a few seconds.", "", nil)
	}

	tmpDir, err := os.MkdirTemp("", "dietapp-blacklist-*")
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	defer os.RemoveAll(tmpDir)

	imagePaths := saveUploadedImages(c, tmpDir)
	if len(imagePaths) == 0 {
		return h.renderBlacklistScan(c, "No image received. Take a photo or choose a file.", "", nil)
	}

	ctx := c.Request().Context()
	settings, err := h.loadSettings(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	const prompt = `Read the ingredient list in this image and return ONLY a JSON array of ingredient names as lowercase strings, one per element.
Strip quantities, percentages, and parenthetical notes. Keep names short (1-5 words).
Example: ["wheat flour","sugar","palm oil"]
No prose, no markdown, no explanation.`

	reply, err := h.visionExtract(ctx, settings, imagePaths,
		"You read ingredient lists and output only JSON.", prompt)
	if err != nil {
		return h.renderBlacklistScan(c, err.Error(), "", nil)
	}
	names := cleanIngredients(parseIngredientJSON(reply))
	if len(names) == 0 {
		return h.renderBlacklistScan(c, "Couldn't read an ingredient list from that photo. Try a sharper, well-lit picture.", "", nil)
	}

	existing, err := h.loadBlacklist(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	existingSet := make(map[string]bool, len(existing))
	for _, b := range existing {
		existingSet[strings.ToLower(strings.TrimSpace(b.Ingredient))] = true
	}

	added := make([]string, 0, len(names))
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || existingSet[key] {
			continue
		}
		if _, err := h.DB.ExecContext(ctx,
			`insert into blacklist (id, user_id, ingredient) values (?, ?, ?)`,
			uuid.NewString(), u.ID, name); err != nil {
			continue
		}
		existingSet[key] = true
		added = append(added, name)
	}

	updated, err := h.loadBlacklist(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	msg := ""
	switch {
	case len(added) == 0:
		msg = "No new ingredients — everything on that list is already blacklisted."
	case len(added) <= 12:
		msg = fmt.Sprintf("Added %d ingredient%s: %s.", len(added), plural(len(added)), strings.Join(added, ", "))
	default:
		msg = fmt.Sprintf("Added %d ingredients, including %s.", len(added), strings.Join(added[:12], ", "))
	}
	return h.renderBlacklistScan(c, "", msg, updated)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (h *Handlers) renderBlacklistScan(c echo.Context, errMsg, msg string, bl []blacklistRow) error {
	c.Response().Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.Templates.RenderPartial(c.Response().Writer, "partials/blacklist_scan_result.html", map[string]any{
		"Error":     errMsg,
		"Message":   msg,
		"Blacklist": bl,
		"Swap":      bl != nil,
	})
}

// saveUploadedImages stores multipart image/images uploads in tmpDir and
// returns their paths. Oversized or unreadable files are skipped.
func saveUploadedImages(c echo.Context, tmpDir string) []string {
	form, err := c.MultipartForm()
	if err != nil || form == nil {
		return nil
	}
	var paths []string
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
			paths = append(paths, dst.Name())
		}
	}
	return paths
}

// visionExtract sends images to the configured provider and returns the raw
// text reply. Errors are already phrased for the UI.
func (h *Handlers) visionExtract(ctx context.Context, settings settingsRow, imagePaths []string, systemPrompt, prompt string) (string, error) {
	src, model, apiKey, baseURL := h.resolveProvider(settings)
	if src == provider.SourceLocal {
		if h.LocalAI == nil || !h.LocalAI.IsVision() {
			return "", errors.New("No local vision model loaded. Visit /models to load a vision-capable model, or switch to a vision-capable cloud model on /profile.")
		}
		resp, err := h.LocalAI.ChatVision(ctx, provider.ChatRequest{Messages: []provider.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		}}, imagePaths)
		if err != nil {
			return "", fmt.Errorf("The AI call failed: %w", err)
		}
		if len(resp.Choices) > 0 {
			return resp.Choices[0].Message.Content, nil
		}
		return "", nil
	}

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
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		mime := http.DetectContentType(data)
		if !strings.HasPrefix(mime, "image/") {
			mime = "image/jpeg"
		}
		images = append(images, provider.ImageInput{Data: data, MimeType: mime})
	}
	resp, err := client.ChatVision(ctx, model, systemPrompt, prompt, images)
	if err != nil {
		return "", fmt.Errorf("The AI call failed: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("The AI provider returned an error: %s", resp.Error.Message)
	}
	if len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content, nil
	}
	return "", nil
}
