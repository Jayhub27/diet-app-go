package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/orionryan/diet-app-go/internal/ai/tools"
	"github.com/orionryan/diet-app-go/internal/auth"
)

// Recipe import: paste a public recipe URL, read the schema.org Recipe JSON-LD
// the site publishes for search engines, and save a normal recipe row. No AI
// call and no API key.

const (
	importMaxBytes  = 2 << 20
	importTimeout   = 10 * time.Second
	importUserAgent = "DietApp/1.0 (+https://github.com/Jayhub27/Diet-App)"
)

var jsonLdScriptRe = regexp.MustCompile(`(?is)<script[^>]*type\s*=\s*["']?application/ld\+json["']?[^>]*>(.*?)</script>`)

type importedRecipe struct {
	Title       string
	Description string
	Servings    int
	PrepMinutes int
	CookMinutes int
	Ingredients []tools.IngredientInput
	Steps       []string
	DietaryTags []string
	SourceURL   string
	Nutrition   map[string]any
}

// RecipeImportPage renders the URL form.
func (h *Handlers) RecipeImportPage(c echo.Context) error {
	return h.page(c, "import", map[string]any{})
}

// RecipeImport fetches and saves a recipe from the submitted URL.
func (h *Handlers) RecipeImport(c echo.Context) error {
	u := auth.CurrentUser(c)
	raw := strings.TrimSpace(c.FormValue("url"))
	if raw == "" {
		return h.page(c, "import", map[string]any{"Error": "Paste a recipe link first."})
	}

	pageHTML, finalURL, err := fetchRecipeHTML(c.Request().Context(), raw)
	if err != nil {
		return h.page(c, "import", map[string]any{"Error": err.Error(), "URL": raw})
	}
	rec, err := parseRecipeHTML(pageHTML, finalURL)
	if err != nil {
		return h.page(c, "import", map[string]any{"Error": err.Error(), "URL": raw})
	}
	id, err := h.saveImportedRecipe(c.Request().Context(), u.ID, rec)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.Redirect(http.StatusSeeOther, "/library/"+id)
}

func (h *Handlers) saveImportedRecipe(ctx context.Context, userID string, rec *importedRecipe) (string, error) {
	id := uuid.NewString()
	ingredients, _ := json.Marshal(rec.Ingredients)
	steps, _ := json.Marshal(rec.Steps)
	tags, _ := json.Marshal(rec.DietaryTags)
	nutrition, _ := json.Marshal(rec.Nutrition)
	_, err := h.DB.ExecContext(ctx,
		`insert into recipes
		 (id, user_id, title, description, servings, prep_minutes, cook_minutes,
		  ingredients_json, steps_json, dietary_tags_json, nutrition_json, source_prompt)
		 values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, rec.Title, rec.Description, rec.Servings, rec.PrepMinutes, rec.CookMinutes,
		string(ingredients), string(steps), string(tags), string(nutrition),
		"Imported from "+rec.SourceURL)
	if err != nil {
		return "", err
	}
	return id, nil
}

// ---- Transport + SSRF guard ----

func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil && v4[0] >= 224 {
		return true
	}
	return false
}

func isBlockedHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || h == "metadata.google.internal" || h == "instance-data" {
		return true
	}
	return strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") ||
		strings.HasSuffix(h, ".internal") || strings.HasSuffix(h, ".home.arpa")
}

// validateImportURL rejects non-http links, private addresses and local hosts.
func validateImportURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("That doesn't look like a valid link.")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("Only http and https links can be imported.")
	}
	if u.User != nil {
		return nil, errors.New("Links with embedded credentials aren't supported.")
	}
	if len(u.String()) > 2048 {
		return nil, errors.New("That link is too long.")
	}
	if isBlockedHost(u.Hostname()) {
		return nil, errors.New("That host can't be imported.")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if isBlockedIP(ip) {
			return nil, errors.New("That host can't be imported.")
		}
		return u, nil
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, errors.New("Couldn't find that website. Check the link and try again.")
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return nil, errors.New("That host can't be imported.")
		}
	}
	return u, nil
}

func fetchRecipeHTML(ctx context.Context, raw string) (string, string, error) {
	target, err := validateImportURL(raw)
	if err != nil {
		return "", "", err
	}

	client := &http.Client{
		Timeout: importTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("That link redirected too many times.")
			}
			if _, err := validateImportURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", "", errors.New("Couldn't build that request.")
	}
	req.Header.Set("user-agent", importUserAgent)
	req.Header.Set("accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")

	res, err := client.Do(req)
	if err != nil {
		return "", "", errors.New("Couldn't reach that page. Check the link and try again.")
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return "", "", errors.New("That site blocked the request. Try another recipe page.")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", "", fmt.Errorf("That page couldn't be loaded (HTTP %d).", res.StatusCode)
	}
	contentType := strings.ToLower(res.Header.Get("content-type"))
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") {
		return "", "", errors.New("That link isn't a recipe web page.")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, importMaxBytes+1))
	if err != nil {
		return "", "", errors.New("Couldn't read that page.")
	}
	if len(body) > importMaxBytes {
		return "", "", errors.New("That page is too large to import.")
	}
	return string(body), res.Request.URL.String(), nil
}

// ---- JSON-LD parsing ----

func isRecipeNode(node map[string]any) bool {
	raw, ok := node["@type"]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case string:
		return strings.Contains(strings.ToLower(v), "recipe")
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && strings.Contains(strings.ToLower(s), "recipe") {
				return true
			}
		}
	}
	return false
}

func collectRecipeNodes(value any, out *[]map[string]any, depth int) {
	if depth > 8 || value == nil {
		return
	}
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			collectRecipeNodes(item, out, depth+1)
		}
	case map[string]any:
		if isRecipeNode(v) {
			*out = append(*out, v)
		}
		for _, child := range v {
			collectRecipeNodes(child, out, depth+1)
		}
	}
}

func firstRecipeNode(pageHTML string) (map[string]any, error) {
	var candidates []map[string]any
	for _, match := range jsonLdScriptRe.FindAllStringSubmatch(pageHTML, -1) {
		if len(match) < 2 {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(strings.TrimSpace(match[1])), &value); err != nil {
			continue
		}
		collectRecipeNodes(value, &candidates, 0)
	}
	if len(candidates) == 0 {
		return nil, errors.New("No recipe data was found on that page. Try the recipe's own page.")
	}
	for _, node := range candidates {
		if arr, ok := node["recipeIngredient"].([]any); ok && len(arr) > 0 {
			return node, nil
		}
	}
	return candidates[0], nil
}

func asText(value any) string {
	switch v := value.(type) {
	case string:
		return stripHTMLText(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case []any:
		for _, item := range v {
			if text := asText(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func asTextSlice(value any) []string {
	var out []string
	switch v := value.(type) {
	case string:
		out = append(out, stripHTMLText(v))
	case []any:
		for _, item := range v {
			out = append(out, asTextSlice(item)...)
		}
	case map[string]any:
		if text := asText(v["name"]); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func stripHTMLText(value string) string {
	lines := htmlToLines(value)
	return strings.Join(lines, " ")
}

var blockTagRe = regexp.MustCompile(`(?is)<\s*/\s*(p|li|div|h[1-6])\s*>|<\s*br\s*/?\s*>`)

func htmlToLines(value string) []string {
	value = blockTagRe.ReplaceAllString(value, "\n")
	value = regexp.MustCompile(`(?is)<[^>]*>`).ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	var out []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func parseLooseInt(value any) int {
	switch v := value.(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case string:
		re := regexp.MustCompile(`\d+(?:\.\d+)?`)
		if m := re.FindString(v); m != "" {
			if f, err := strconv.ParseFloat(m, 64); err == nil && f > 0 {
				return int(f)
			}
		}
	case []any:
		for _, item := range v {
			if n := parseLooseInt(item); n > 0 {
				return n
			}
		}
	}
	return 0
}

// durationMinutes handles ISO-8601 ("PT1H30M") and human ("1 hr 20 min").
func durationMinutes(value any) int {
	s, ok := value.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return 0
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToUpper(s), "P") {
		re := regexp.MustCompile(`(?i)P(?:(\d+(?:\.\d+)?)D)?(?:T(?:(\d+(?:\.\d+)?)H)?(?:(\d+(?:\.\d+)?)M)?(?:(\d+(?:\.\d+)?)S)?)?`)
		if m := re.FindStringSubmatch(s); m != nil {
			total := 0.0
			units := []float64{1440, 60, 1, 1.0 / 60.0}
			for i := 1; i <= 4 && i < len(m); i++ {
				if m[i] != "" {
					if f, err := strconv.ParseFloat(m[i], 64); err == nil {
						total += f * units[i-1]
					}
				}
			}
			return int(total + 0.5)
		}
	}
	hours := regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:h|hr|hrs|hour|hours)\b`).FindStringSubmatch(s)
	mins := regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:m|min|mins|minute|minutes)\b`).FindStringSubmatch(s)
	if hours != nil || mins != nil {
		h, _ := strconv.ParseFloat(firstMatch(hours), 64)
		m, _ := strconv.ParseFloat(firstMatch(mins), 64)
		return int(h*60 + m + 0.5)
	}
	return 0
}

func firstMatch(m []string) string {
	if len(m) > 1 {
		return m[1]
	}
	return "0"
}

func flattenInstructions(value any, out *[]string, depth int) {
	if depth > 6 || value == nil || len(*out) >= 80 {
		return
	}
	switch v := value.(type) {
	case string:
		*out = append(*out, htmlToLines(v)...)
	case []any:
		for _, item := range v {
			flattenInstructions(item, out, depth+1)
		}
	case map[string]any:
		if nested, ok := v["itemListElement"]; ok {
			flattenInstructions(nested, out, depth+1)
			return
		}
		if text, ok := v["text"]; ok {
			flattenInstructions(text, out, depth+1)
			return
		}
		if name, ok := v["name"]; ok {
			flattenInstructions(name, out, depth+1)
		}
	}
}

var dietTagMap = map[string]string{
	"vegandiet":       "vegan",
	"vegetariandiet":  "vegetarian",
	"pescatariandiet": "pescatarian",
	"glutenfreediet":  "gluten-free",
	"dairyfreediet":   "dairy-free",
	"lowcaloriediet":  "low-calorie",
	"lowfatdiet":      "low-fat",
	"lowlactosediet":  "low-lactose",
	"lowsaltdiet":     "low-sodium",
	"diabeticdiet":    "diabetic",
	"halaldiet":       "halal",
	"kosherdiet":      "kosher",
	"lowfodmapdiet":   "low-fodmap",
}

func recipeDietaryTags(node map[string]any) []string {
	var tags []string
	for _, raw := range asTextSlice(node["suitableForDiet"]) {
		key := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(raw, "http://"), "https://"))
		if idx := strings.Index(key, "/"); idx >= 0 {
			key = key[idx+1:]
		}
		key = regexp.MustCompile(`[^a-z]`).ReplaceAllString(key, "")
		if tag, ok := dietTagMap[key]; ok {
			tags = append(tags, tag)
		}
	}
	for _, raw := range asTextSlice(node["recipeCategory"]) {
		for _, part := range strings.Split(raw, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part != "" && len(tags) < 4 {
				tags = append(tags, part)
			}
		}
	}
	seen := map[string]bool{}
	out := tags[:0]
	for _, tag := range tags {
		if !seen[tag] && len(out) < 4 {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out
}

func parseRecipeHTML(pageHTML, sourceURL string) (*importedRecipe, error) {
	node, err := firstRecipeNode(pageHTML)
	if err != nil {
		return nil, err
	}
	title := asText(node["name"])
	if title == "" {
		title = asText(node["headline"])
	}
	if title == "" {
		return nil, errors.New("That page has recipe data but no title.")
	}

	var ingredientLines []string
	switch v := node["recipeIngredient"].(type) {
	case []any:
		ingredientLines = asTextSlice(v)
	case string:
		ingredientLines = []string{v}
	}
	if len(ingredientLines) == 0 {
		return nil, errors.New("That recipe page didn't list any ingredients.")
	}
	var ingredients []tools.IngredientInput
	for _, line := range ingredientLines {
		if ing, ok := parseIngredientLine(line); ok {
			ingredients = append(ingredients, ing)
		}
		if len(ingredients) >= 100 {
			break
		}
	}
	if len(ingredients) == 0 {
		return nil, errors.New("Couldn't read the ingredient list on that page.")
	}

	var steps []string
	flattenInstructions(node["recipeInstructions"], &steps, 0)
	if len(steps) > 80 {
		steps = steps[:80]
	}

	servings := parseLooseInt(node["recipeYield"])
	if servings <= 0 {
		servings = 4
	}
	prep := durationMinutes(node["prepTime"])
	cook := durationMinutes(node["cookTime"])
	if cook == 0 {
		cook = durationMinutes(node["totalTime"])
	}

	rec := &importedRecipe{
		Title:       truncateRunes(title, 160),
		Description: truncateRunes(asText(node["description"]), 500),
		Servings:    servings,
		PrepMinutes: prep,
		CookMinutes: cook,
		Ingredients: ingredients,
		Steps:       steps,
		DietaryTags: recipeDietaryTags(node),
		SourceURL:   sourceURL,
		Nutrition:   tools.EstimateIngredientsNutrition(ingredients, servings),
	}
	return rec, nil
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// ---- Ingredient line parser ----

var unicodeFractions = map[rune]float64{
	'¼': 0.25, '½': 0.5, '¾': 0.75, '⅓': 1.0 / 3, '⅔': 2.0 / 3,
	'⅕': 0.2, '⅖': 0.4, '⅗': 0.6, '⅘': 0.8, '⅙': 1.0 / 6,
	'⅚': 5.0 / 6, '⅛': 0.125, '⅜': 0.375, '⅝': 0.625, '⅞': 0.875,
}

var unitAliases = map[string]string{
	"g": "g", "gram": "g", "grams": "g", "gr": "g",
	"kg": "kg", "kilogram": "kg", "kilograms": "kg",
	"ml": "ml", "milliliter": "ml", "milliliters": "ml", "millilitre": "ml", "millilitres": "ml",
	"l": "l", "liter": "l", "liters": "l", "litre": "l", "litres": "l",
	"cup": "cup", "cups": "cup",
	"tbsp": "tbsp", "tablespoon": "tbsp", "tablespoons": "tbsp", "tbs": "tbsp",
	"tsp": "tsp", "teaspoon": "tsp", "teaspoons": "tsp",
	"oz": "oz", "ounce": "oz", "ounces": "oz",
	"lb": "lb", "lbs": "lb", "pound": "lb", "pounds": "lb",
	"clove": "clove", "cloves": "clove",
	"can": "can", "cans": "can", "jar": "jar", "jars": "jar",
	"packet": "packet", "packets": "packet", "pkg": "packet", "package": "packet", "packages": "packet",
	"bottle": "bottle", "bottles": "bottle", "slice": "slice", "slices": "slice",
	"piece": "piece", "pieces": "piece", "pinch": "pinch", "pinches": "pinch",
	"bunch": "bunch", "bunches": "bunch", "sprig": "sprig", "sprigs": "sprig",
	"stalk": "stalk", "stalks": "stalk", "handful": "handful", "handfuls": "handful",
	"stick": "stick", "sticks": "stick", "head": "head", "heads": "head",
	"fillet": "fillet", "fillets": "fillet", "sheet": "sheet", "sheets": "sheet",
	"scoop": "scoop", "scoops": "scoop",
}

var leadingAmountRe = regexp.MustCompile(`^(\d+\s+\d+\s*/\s*\d+|\d+\s+[¼½¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞]|\d+\s*/\s*\d+|\d+(?:[.,]\d+)?|[¼½¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞])`)
var rangeRe = regexp.MustCompile(`^\s*(?:-|–|—|to)\s*(?:\d+\s*/\s*\d+|\d+(?:[.,]\d+)?|[¼½¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞])\s*`)
var prepSuffixRe = regexp.MustCompile(`(?i)\s+(?:finely|roughly|thinly|coarsely|freshly)?\s*(?:chopped|sliced|diced|minced|grated|crushed|peeled|trimmed|drained|rinsed|halved|quartered|shredded|beaten|melted|softened|divided)\b.*$`)

func parseAmountAtStart(input string) (float64, string, bool) {
	match := leadingAmountRe.FindStringSubmatch(input)
	if match == nil {
		return 0, input, false
	}
	raw := match[1]
	amount := 0.0
	if parts := strings.Fields(raw); len(parts) == 2 && len([]rune(parts[1])) > 0 {
		if frac, ok := unicodeFractions[[]rune(parts[1])[0]]; ok {
			whole, _ := strconv.ParseFloat(parts[0], 64)
			amount = whole + frac
		}
	}
	if amount > 0 {
		rest := input[len(match[0]):]
		rest = rangeRe.ReplaceAllString(rest, "")
		return amount, strings.TrimSpace(rest), true
	}
	switch {
	case strings.Contains(raw, "/"):
		parts := strings.Fields(raw)
		fraction := parts[len(parts)-1]
		nums := strings.Split(fraction, "/")
		if len(nums) == 2 {
			num, _ := strconv.ParseFloat(nums[0], 64)
			den, _ := strconv.ParseFloat(nums[1], 64)
			if den != 0 {
				amount = num / den
			}
		}
		if len(parts) == 2 {
			whole, _ := strconv.ParseFloat(parts[0], 64)
			amount += whole
		}
	case len([]rune(raw)) == 1:
		if frac, ok := unicodeFractions[[]rune(raw)[0]]; ok {
			amount = frac
		} else {
			amount, _ = strconv.ParseFloat(raw, 64)
		}
	default:
		amount, _ = strconv.ParseFloat(strings.Replace(raw, ",", ".", 1), 64)
	}
	if amount <= 0 {
		return 0, input, false
	}
	rest := input[len(match[0]):]
	rest = rangeRe.ReplaceAllString(rest, "")
	return amount, strings.TrimSpace(rest), true
}

func matchUnit(rest string) (string, string, bool) {
	words := strings.Fields(strings.ToLower(rest))
	if len(words) == 0 {
		return "", rest, false
	}
	if canonical, ok := unitAliases[strings.Trim(words[0], ".")]; ok {
		remaining := strings.TrimSpace(rest[len(words[0]):])
		return canonical, remaining, true
	}
	return "", rest, false
}

// parseIngredientLine turns "1 ½ cups all-purpose flour, sifted" into the
// shared IngredientInput shape.
func parseIngredientLine(line string) (tools.IngredientInput, bool) {
	rest := strings.TrimSpace(html.UnescapeString(line))
	rest = strings.TrimLeft(rest, "-•* \t")
	rest = strings.Join(strings.Fields(rest), " ")
	if rest == "" {
		return tools.IngredientInput{}, false
	}

	amount := 1.0
	unit := ""
	if parsedAmount, remaining, ok := parseAmountAtStart(rest); ok {
		amount = parsedAmount
		rest = remaining
		// Drop everything after a comma (prep notes).
		if idx := strings.Index(rest, ","); idx >= 0 {
			rest = rest[:idx]
		}
		// Package-size parenthetical: "1 (14 oz) can diced tomatoes".
		if strings.HasPrefix(rest, "(") {
			if close := strings.Index(rest, ")"); close > 0 {
				rest = strings.TrimSpace(rest[close+1:])
			}
		}
		// Size adjective before a unit: "1 small bunch parsley".
		fields := strings.Fields(rest)
		if len(fields) >= 2 {
			switch strings.ToLower(fields[0]) {
			case "small", "medium", "large", "whole":
				if canonical, ok := unitAliases[strings.Trim(fields[1], ".")]; ok {
					unit = canonical
					rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[0]+" "+fields[1]))
				}
			}
		}
		if unit == "" {
			if canonical, remaining, ok := matchUnit(rest); ok {
				unit = canonical
				rest = remaining
			}
		}
		// Unit after the ingredient noun: "1 garlic clove".
		if unit == "" {
			for _, word := range []string{"clove", "cloves", "slice", "slices", "can", "cans", "jar", "jars", "packet", "packets", "sprig", "sprigs", "stalk", "stalks", "head", "heads", "bunch", "bunches"} {
				re := regexp.MustCompile(`(?i)^([A-Za-z][A-Za-z' -]*?)\s+` + word + `\b`)
				if m := re.FindStringSubmatch(rest); m != nil {
					unit = unitAliases[word]
					rest = strings.TrimSpace(m[1] + " " + strings.TrimPrefix(strings.TrimSpace(rest[len(m[0]):]), word))
					break
				}
			}
		}
	}

	name := strings.TrimSpace(rest)
	name = regexp.MustCompile(`(?i)^of\s+`).ReplaceAllString(name, "")
	name = prepSuffixRe.ReplaceAllString(name, "")
	name = regexp.MustCompile(`\s+zest only$`).ReplaceAllString(name, " zest")
	name = strings.Join(strings.Fields(name), " ")
	name = strings.TrimRight(name, ".;:")
	if name == "" {
		name = strings.TrimSpace(line)
	}
	if name == "" {
		return tools.IngredientInput{}, false
	}
	return tools.IngredientInput{
		Name:   truncateRunes(name, 120),
		Amount: amount,
		Unit:   unit,
	}, true
}
