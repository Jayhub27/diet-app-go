package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Real-product recommendations backed by Open Food Facts (free, no API key).
// Mirrors the TypeScript app's src/lib/ai/products.ts:
//
//  1. search.openfoodfacts.org finds candidate products (names, brands,
//     labels and allergen tags — the search index has no ingredient text);
//  2. world.openfoodfacts.org/api/v2/product/<code> enriches the top
//     candidates with the ingredient list, "may contain" traces and stores.
//
// Evaluation reuses the restriction keyword lists (restrictions.go) with a
// word-start boundary plus a product exception layer: raw manufacturer text
// ("flaxmilk", "rhamnosus", "cream of tartar") would otherwise trip the
// coarser substring matching that works fine for model-written recipe
// ingredient names. Extend, never weaken.

// Base URLs are variables so tests can point them at an httptest server.
var (
	offSearchURL  = "https://search.openfoodfacts.org/search"
	offProductURL = "https://world.openfoodfacts.org/api/v2/product"
)

const (
	offUserAgent = "DietApp-Lite/1.0 (+https://github.com/Jayhub27/Diet-App)"
	offTimeout   = 8 * time.Second
)

var offProductFields = strings.Join([]string{
	"code", "product_name", "brands", "quantity",
	"ingredients_text", "ingredients_text_en", "allergens_tags", "traces_tags",
	"labels_tags", "stores", "image_front_small_url", "nutriscore_grade", "lang",
}, ",")

// ProductCandidate is one product before/after enrichment. JSON tags match
// the TypeScript EvaluatedProduct payload so both apps stay in sync.
type ProductCandidate struct {
	Code                string   `json:"code"`
	Name                string   `json:"name"`
	Brand               string   `json:"brand,omitempty"`
	Quantity            string   `json:"quantity,omitempty"`
	IngredientsText     string   `json:"ingredientsText,omitempty"`
	IngredientsLanguage string   `json:"ingredientsLanguage,omitempty"`
	Allergens           []string `json:"allergens"`
	Traces              []string `json:"traces"`
	Labels              []string `json:"labels"`
	Stores              string   `json:"stores,omitempty"`
	ImageURL            string   `json:"imageUrl,omitempty"`
	NutriscoreGrade     string   `json:"nutriscoreGrade,omitempty"`
	URL                 string   `json:"url"`
}

// ProductViolation is a single conflict between a product and the user's
// restrictions or blacklist.
type ProductViolation struct {
	Kind        string   `json:"kind"`        // allergen | trace | ingredient | blacklist
	Restriction *string  `json:"restriction"` // null for blacklist hits
	Severity    Severity `json:"severity"`
	Detail      string   `json:"detail"`
}

// EvaluatedProduct is a candidate plus its fit verdict.
type EvaluatedProduct struct {
	ProductCandidate
	Verdict    string             `json:"verdict"` // safe | check | unsafe
	Violations []ProductViolation `json:"violations"`
	Notes      []string           `json:"notes"`
}

type productSearchResponse struct {
	Query    string             `json:"query"`
	Products []EvaluatedProduct `json:"products"`
	Fallback bool               `json:"fallback"`
}

// BarcodeLookupResponse mirrors the TS lookup_product_by_barcode output.
type BarcodeLookupResponse struct {
	Barcode string            `json:"barcode"`
	Status  string            `json:"status"` // found | not_found | invalid_barcode | source_unavailable
	Product *EvaluatedProduct `json:"product"`
}

// ---------------------------------------------------------------------------
// Allergen tags -> restrictions
// ---------------------------------------------------------------------------

type allergenRule struct {
	label        string
	restrictions []string
	test         func(tag string) bool
}

var (
	nutExcludeRe = regexp.MustCompile(`coconut|nutmeg|butternut|doughnut`)
	nutNameRe    = regexp.MustCompile(`almond|hazelnut|walnut|cashew|pecan|pistachio|macadamia|brazil`)
	fishRe       = regexp.MustCompile(`fish|anchov|salmon|tuna|cod|sardine|mackerel|trout|seafood`)
	shellfishRe  = regexp.MustCompile(`crustacean|mollusc|shellfish|shrimp|prawn|lobster|crab|clam|mussel|oyster|squid|octopus|cuttlefish`)
)

var allergenRules = []allergenRule{
	{
		label:        "gluten",
		restrictions: []string{"gluten-free"},
		test: func(t string) bool {
			return strings.Contains(t, "gluten") || strings.Contains(t, "wheat") ||
				strings.Contains(t, "barley") || strings.Contains(t, "rye")
		},
	},
	{
		label:        "milk",
		restrictions: []string{"dairy-free", "vegan"},
		test: func(t string) bool {
			for _, kw := range []string{"milk", "lactose", "dairy", "whey", "casein", "cream", "butter", "ghee"} {
				if strings.Contains(t, kw) {
					return true
				}
			}
			return false
		},
	},
	{
		label:        "egg",
		restrictions: []string{"vegan"},
		test:         func(t string) bool { return strings.Contains(t, "egg") },
	},
	{
		label:        "peanuts",
		restrictions: []string{"nut-free"},
		test:         func(t string) bool { return strings.Contains(t, "peanut") },
	},
	{
		// OFF uses tags like "en:nuts", "en:tree-nuts", "en:hazelnuts".
		// Coconut is botanically not a tree nut (and "coconut" contains "nut").
		label:        "nuts",
		restrictions: []string{"nut-free"},
		test: func(t string) bool {
			return nutNameRe.MatchString(t) || (strings.Contains(t, "nut") && !nutExcludeRe.MatchString(t))
		},
	},
	{
		label:        "fish",
		restrictions: []string{"vegan", "vegetarian"},
		test:         fishRe.MatchString,
	},
	{
		label:        "shellfish",
		restrictions: []string{"vegan", "vegetarian", "kosher"},
		test:         shellfishRe.MatchString,
	},
	{
		label:        "pork",
		restrictions: []string{"halal", "kosher", "pescatarian", "vegetarian", "vegan"},
		test: func(t string) bool {
			return strings.Contains(t, "pork") || strings.Contains(t, "lard") || strings.Contains(t, "bacon")
		},
	},
	{
		label:        "alcohol",
		restrictions: []string{"halal"},
		test: func(t string) bool {
			for _, kw := range []string{"alcohol", "wine", "beer", "liquor"} {
				if strings.Contains(t, kw) {
					return true
				}
			}
			return false
		},
	},
}

// matchAllergenTag matches a raw OFF tag (e.g. "en:cultured-nonfat-milk")
// against the allergen rules. Returns every rule that applies so one tag can
// violate several restrictions (milk violates dairy-free and vegan).
func matchAllergenTag(rawTag string) []allergenRule {
	tag := strings.ToLower(rawTag)
	if len(tag) >= 3 && tag[2] == ':' {
		tag = tag[3:]
	}
	tag = strings.ReplaceAll(tag, "_", "-")
	out := make([]allergenRule, 0, 2)
	for _, rule := range allergenRules {
		if rule.test(tag) {
			out = append(out, rule)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Ingredient text handling
// ---------------------------------------------------------------------------

var (
	ingredientSplitRe = regexp.MustCompile(`[,;()\[\]{}]+|\s+-\s+|\.\s+`)
	percentRe         = regexp.MustCompile(`\b\d+(?:[.,]\d+)?\s*%`)
	pureNumberRe      = regexp.MustCompile(`^\d+(?:[.,]\d+)?$`)
	leadingLabelRe    = regexp.MustCompile(`^(?i:ingredients?|contains|may contain)\s*:?\s+`)
)

var groupWords = map[string]bool{
	"ingredient": true, "ingredients": true, "may contain": true, "contains": true,
	"allergen": true, "allergens": true, "nutrition information": true,
	"nutritional information": true, "cereal": true, "cereals": true,
}

// splitIngredientText splits a printed ingredient list into components so the
// keyword matching runs per ingredient instead of on the whole paragraph (a
// paragraph-level match would wrongly exempt "wheat flour" when any exception
// word appears anywhere in the text). Percentages are removed first so
// decimals survive, and group words ("Ingredients", "Cereals") are dropped.
func splitIngredientText(text string) []string {
	cleaned := percentRe.ReplaceAllString(text, " ")
	parts := ingredientSplitRe.Split(cleaned, -1)
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		p := strings.Join(strings.Fields(part), " ")
		p = leadingLabelRe.ReplaceAllString(p, "")
		p = strings.TrimSpace(strings.TrimRight(p, ".:"))
		if len(p) <= 1 || pureNumberRe.MatchString(p) || groupWords[strings.ToLower(p)] {
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) >= 200 {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Product-tuned keyword matching
// ---------------------------------------------------------------------------

// productBaseExceptions ports the recipe safety gate's SAFE_EXCEPTIONS so
// product checking behaves like the TypeScript app.
var productBaseExceptions = map[string]*regexp.Regexp{
	"vegan":       regexp.MustCompile(`(?i)coconut (milk|cream|yogurt|oil)|(oat|soy|almond|rice|cashew|hemp|pea)[- ]?milk|(peanut|almond|cashew|cocoa|apple|nut|seed) butter|(vegan|plant|nut|cashew|dairy[- ]free) (cheese|butter|yogurt)|(oat|soy|coconut) (cream|yogurt)|eggplant|egg(less| substitute| replacer)|flax egg|chia egg|aquafaba|(vegetable|mushroom|vegan) stock|meat(less|[- ]free)|no[- ]meat|meat substitute|plant[- ]based`),
	"vegetarian":  regexp.MustCompile(`(?i)(vegetable|mushroom|vegan) (stock|broth)|meat(less|[- ]free)|no[- ]meat|meat substitute|eggplant`),
	"pescatarian": regexp.MustCompile(`(?i)(vegetable|mushroom|vegan) (stock|broth)|meat(less|[- ]free)|no[- ]meat|eggplant`),
	"keto":        regexp.MustCompile(`(?i)sugar[- ]?free|no[- ]sugar|unsweetened|cauliflower rice|zucchini noodle|(almond|coconut|chickpea|rice) flour`),
	"gluten-free": regexp.MustCompile(`(?i)gluten[- ]?free|buckwheat|tamari|(almond|rice|chickpea|coconut|corn|maize|potato|tapioca) flour|(rice|glass) noodle|corn tortilla`),
	"dairy-free":  regexp.MustCompile(`(?i)coconut (milk|cream|yogurt|oil)|(oat|soy|almond|rice|cashew|hemp|pea)[- ]?milk|(oat|soy|coconut) cream|(peanut|almond|cashew|cocoa|apple|nut|seed) butter|(vegan|plant) (butter|cheese|yogurt)|dairy[- ]?free|milk[- ]?free`),
	"nut-free":    regexp.MustCompile(`(?i)nut[- ]?free|coconut|butternut|nutmeg|water chestnut|doughnut`),
	"low-sodium":  regexp.MustCompile(`(?i)unsalted|no[- ]?salt|salt[- ]?free|low[- ]sodium`),
	"halal":       regexp.MustCompile(`(?i)alcohol[- ]?free|non[- ]alcoholic|halal`),
	"kosher":      regexp.MustCompile(`(?i)kosher`),
	"low-fodmap":  regexp.MustCompile(`(?i)garlic[- ]infused|garlic[- ]free|onion[- ]free`),
}

// productExceptions adds raw-label exceptions on top of the base list.
// Conservative by design: an entry must be unambiguously safe.
var productExceptions = map[string]*regexp.Regexp{
	"vegan":       regexp.MustCompile(`(?i)coconut ?(milk|cream|yogurt|oil|meat|water)|flax ?milk|non[- ]?dairy|cream of tartar|butter ?beans?|shea butter|hamburger (bun|buns|roll|rolls)`),
	"vegetarian":  regexp.MustCompile(`(?i)coconut ?(meat|water)|hamburger (bun|buns|roll|rolls)`),
	"pescatarian": regexp.MustCompile(`(?i)coconut ?(meat|water)|hamburger (bun|buns|roll|rolls)`),
	"dairy-free":  regexp.MustCompile(`(?i)coconut ?(milk|cream|yogurt|oil)|flax ?milk|non[- ]?dairy|lactose[- ]?free|cream of tartar|butter ?beans?|shea butter`),
	"gluten-free": regexp.MustCompile(`(?i)(oat|cassava|teff|sorghum|millet|quinoa|amaranth|arrowroot|brown rice|gram|lentil|pea|soy|buckwheat|chestnut|hazelnut) flour|malt(ose|odextrin|itol)`),
	"nut-free":    regexp.MustCompile(`(?i)nutritional yeast`),
	"keto":        regexp.MustCompile(`(?i)sugar ?snap|rice vinegar`),
	"low-fodmap":  regexp.MustCompile(`(?i)apple cider vinegar`),
}

func exemptForProduct(component, restriction string) bool {
	if re, ok := productBaseExceptions[restriction]; ok && re.MatchString(component) {
		return true
	}
	if re, ok := productExceptions[restriction]; ok && re.MatchString(component) {
		return true
	}
	return false
}

func isASCIINum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// keywordStartsWord reports whether keyword occurs at a word start in
// component. Unlike the recipe gate (substring matching on model-written
// names), keywords must start a word here so "rhamnosus" doesn't match "ham".
// A trailing boundary is intentionally not required so "buttermilk" still
// matches "butter".
func keywordStartsWord(component, keyword string) bool {
	lower := strings.ToLower(component)
	kw := strings.ToLower(keyword)
	start := 0
	for {
		idx := strings.Index(lower[start:], kw)
		if idx < 0 {
			return false
		}
		pos := start + idx
		if pos == 0 || !isASCIINum(lower[pos-1]) {
			return true
		}
		start = pos + 1
	}
}

type ingredientViolation struct {
	component   string
	restriction string
	severity    Severity
	reason      string
}

func findIngredientViolations(components []string, restrictions []Restriction) []ingredientViolation {
	out := make([]ingredientViolation, 0)
	seen := make(map[string]bool)
	for _, component := range components {
		for _, r := range restrictions {
			name := strings.ToLower(r.Name)
			rule, ok := forbidden[name]
			if !ok {
				continue
			}
			if exemptForProduct(component, name) {
				continue
			}
			for _, kw := range rule.keywords {
				if !keywordStartsWord(component, kw) {
					continue
				}
				key := name + "|" + component
				if !seen[key] {
					seen[key] = true
					out = append(out, ingredientViolation{
						component:   component,
						restriction: r.Name,
						severity:    r.Severity,
						reason:      rule.reason,
					})
				}
				break
			}
		}
	}
	return out
}

// matchBlacklistTerm is a whole-word (plus simple plural) match so "ham"
// doesn't match "graham" — mirrors the TypeScript matchesBlacklistTerm.
func matchBlacklistTerm(ingredientName, term string) bool {
	t := strings.ToLower(strings.TrimSpace(term))
	if t == "" {
		return false
	}
	name := strings.ToLower(ingredientName)
	stems := []string{t, strings.TrimSuffix(t, "s"), strings.TrimSuffix(t, "es")}
	for _, stem := range stems {
		if stem == "" {
			continue
		}
		re := regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(stem) + `(s|es)?([^a-z0-9]|$)`)
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Evaluation
// ---------------------------------------------------------------------------

var verdictRank = map[string]int{"safe": 0, "check": 1, "unsafe": 2}

type labelNote struct {
	test        func(label string) bool
	restriction string
	note        string
}

var labelNotes = []labelNote{
	{func(l string) bool { return l == "gluten-free" || l == "no-gluten" }, "gluten-free", "labeled gluten-free"},
	{func(l string) bool { return l == "vegan" }, "vegan", "labeled vegan"},
	{func(l string) bool { return l == "vegetarian" }, "vegetarian", "labeled vegetarian"},
	{func(l string) bool {
		return l == "dairy-free" || l == "lactose-free" || l == "no-lactose" || l == "milk-free"
	}, "dairy-free", "labeled lactose/dairy-free"},
	{func(l string) bool { return l == "halal" }, "halal", "labeled halal"},
	{func(l string) bool { return l == "kosher" }, "kosher", "labeled kosher"},
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func strPtr(s string) *string { return &s }

func dedupeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// evaluateProduct judges one product against the user's restrictions and
// blacklist. Verdicts:
//
//   - unsafe — a conflict was detected (allergen tag, ingredient keyword or
//     blacklist hit, or a "may contain" trace at anaphylaxis severity);
//   - check  — no conflict detected, but the product needs a label check
//     (missing/non-English ingredient data, or a "may contain" trace);
//   - safe   — the English ingredient list and allergen tags were scanned and
//     no conflict was found.
func evaluateProduct(p ProductCandidate, restrictions []Restriction, blacklist []string) (string, []ProductViolation, []string) {
	byRestriction := make(map[string]Restriction, len(restrictions))
	for _, r := range restrictions {
		byRestriction[r.Name] = r
	}

	violations := make([]ProductViolation, 0)
	notes := make([]string, 0)
	seen := make(map[string]bool)
	push := func(v ProductViolation) {
		key := v.Kind + "|" + derefString(v.Restriction) + "|" + v.Detail
		if seen[key] {
			return
		}
		seen[key] = true
		violations = append(violations, v)
	}

	// 1. Regulated allergen tags — a hard conflict at any severity.
	for _, tag := range p.Allergens {
		for _, rule := range matchAllergenTag(tag) {
			for _, name := range rule.restrictions {
				entry, ok := byRestriction[name]
				if !ok {
					continue
				}
				push(ProductViolation{
					Kind:        "allergen",
					Restriction: strPtr(name),
					Severity:    entry.Severity,
					Detail:      "contains " + rule.label,
				})
			}
		}
	}

	// 2. "May contain" traces — life-threatening only at anaphylaxis severity.
	for _, tag := range p.Traces {
		for _, rule := range matchAllergenTag(tag) {
			for _, name := range rule.restrictions {
				entry, ok := byRestriction[name]
				if !ok {
					continue
				}
				if entry.Severity == SeverityAnaphylaxis {
					push(ProductViolation{
						Kind:        "trace",
						Restriction: strPtr(name),
						Severity:    entry.Severity,
						Detail:      "may contain " + rule.label + " — refused at anaphylaxis severity",
					})
				} else {
					notes = append(notes, "\"May contain "+rule.label+"\" — cross-contamination risk; check the packaging")
				}
			}
		}
	}

	// 3. Ingredient-by-ingredient keyword scan (product-tuned matching).
	if p.IngredientsText != "" {
		components := splitIngredientText(p.IngredientsText)
		for _, v := range findIngredientViolations(components, restrictions) {
			push(ProductViolation{
				Kind:        "ingredient",
				Restriction: strPtr(v.restriction),
				Severity:    v.severity,
				Detail:      "ingredients mention \"" + v.component + "\" — " + v.reason,
			})
		}
		for _, entry := range blacklist {
			hit := ""
			for _, component := range components {
				if matchBlacklistTerm(component, entry) {
					hit = component
					break
				}
			}
			if hit != "" {
				push(ProductViolation{
					Kind:        "blacklist",
					Restriction: nil,
					Severity:    SeverityAvoid,
					Detail:      "ingredients mention \"" + hit + "\" — on the prohibited list (\"" + entry + "\")",
				})
			}
		}
	} else {
		notes = append(notes, "no ingredient list on file")
	}

	// 4. Positive labels for the user's restrictions.
	labels := make([]string, 0, len(p.Labels))
	for _, l := range p.Labels {
		l = strings.ToLower(l)
		if len(l) >= 3 && l[2] == ':' {
			l = l[3:]
		}
		labels = append(labels, l)
	}
	for _, ln := range labelNotes {
		if _, ok := byRestriction[ln.restriction]; !ok {
			continue
		}
		for _, l := range labels {
			if ln.test(l) {
				notes = append(notes, ln.note)
				break
			}
		}
	}

	// 5. Language note + verdict.
	ingredientLanguageKnownEnglish := p.IngredientsLanguage == "en"
	if p.IngredientsText != "" && !ingredientLanguageKnownEnglish {
		lang := p.IngredientsLanguage
		if lang == "" {
			lang = "an unknown language"
		}
		notes = append(notes, "ingredient list is in \""+lang+"\" — allergen tags were checked, but read the package label")
	}

	verdict := "safe"
	switch {
	case len(violations) > 0:
		verdict = "unsafe"
	case p.IngredientsText == "":
		verdict = "check"
	case !ingredientLanguageKnownEnglish:
		verdict = "check"
	default:
		for _, n := range notes {
			if strings.HasPrefix(n, "\"May contain") {
				verdict = "check"
				break
			}
		}
	}

	notes = dedupeStrings(notes)
	if len(notes) > 6 {
		notes = notes[:6]
	}
	if len(violations) > 8 {
		violations = violations[:8]
	}
	return verdict, violations, notes
}

func evaluateProductJSON(p ProductCandidate, restrictions []Restriction, blacklist []string) EvaluatedProduct {
	verdict, violations, notes := evaluateProduct(p, restrictions, blacklist)
	return EvaluatedProduct{ProductCandidate: p, Verdict: verdict, Violations: violations, Notes: notes}
}

// sortProducts orders safe -> check -> unsafe, richer data ahead.
func sortProducts(products []EvaluatedProduct) {
	sort.SliceStable(products, func(i, j int) bool {
		if verdictRank[products[i].Verdict] != verdictRank[products[j].Verdict] {
			return verdictRank[products[i].Verdict] < verdictRank[products[j].Verdict]
		}
		hasI := products[i].IngredientsText != ""
		hasJ := products[j].IngredientsText != ""
		return hasI && !hasJ
	})
}

// ---------------------------------------------------------------------------
// Open Food Facts transport
// ---------------------------------------------------------------------------

func productPageURL(code string) string {
	return "https://world.openfoodfacts.org/product/" + url.PathEscape(code)
}

func jsonStr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

func jsonStringArray(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// normalizeJoined handles OFF returning brands/stores as an array in search
// hits and a comma-joined string in v2.
func normalizeJoined(v any) string {
	switch t := v.(type) {
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case string:
		return strings.TrimSpace(t)
	}
	return ""
}

// mapSearchHit maps a search-index hit into a candidate (no ingredient text).
func mapSearchHit(raw map[string]any) (ProductCandidate, bool) {
	code := jsonStr(raw, "code")
	name := jsonStr(raw, "product_name")
	if name == "" {
		name = jsonStr(raw, "product_name_en")
	}
	if code == "" || name == "" {
		return ProductCandidate{}, false
	}
	return ProductCandidate{
		Code:            code,
		Name:            name,
		Brand:           normalizeJoined(raw["brands"]),
		Allergens:       jsonStringArray(raw, "allergens_tags"),
		Traces:          []string{},
		Labels:          jsonStringArray(raw, "labels_tags"),
		Stores:          normalizeJoined(raw["stores"]),
		ImageURL:        jsonStr(raw, "image_front_small_url"),
		NutriscoreGrade: jsonStr(raw, "nutriscore_grade"),
		URL:             productPageURL(code),
	}, true
}

// mapProductDetail merges a v2 product payload over the search-hit fallback.
func mapProductDetail(raw map[string]any, fallback *ProductCandidate) (ProductCandidate, bool) {
	code := jsonStr(raw, "code")
	name := jsonStr(raw, "product_name")
	if code == "" && fallback != nil {
		code = fallback.Code
	}
	if name == "" && fallback != nil {
		name = fallback.Name
	}
	if code == "" || name == "" {
		return ProductCandidate{}, false
	}

	textEn := jsonStr(raw, "ingredients_text_en")
	text := jsonStr(raw, "ingredients_text")
	ingredientsText := textEn
	ingredientsLanguage := ""
	if textEn != "" {
		ingredientsLanguage = "en"
	} else {
		ingredientsText = text
		ingredientsLanguage = jsonStr(raw, "lang")
	}

	out := ProductCandidate{
		Code:                code,
		Name:                name,
		Brand:               normalizeJoined(raw["brands"]),
		Quantity:            jsonStr(raw, "quantity"),
		IngredientsText:     ingredientsText,
		IngredientsLanguage: ingredientsLanguage,
		Allergens:           jsonStringArray(raw, "allergens_tags"),
		Traces:              jsonStringArray(raw, "traces_tags"),
		Labels:              jsonStringArray(raw, "labels_tags"),
		Stores:              normalizeJoined(raw["stores"]),
		ImageURL:            jsonStr(raw, "image_front_small_url"),
		NutriscoreGrade:     jsonStr(raw, "nutriscore_grade"),
		URL:                 productPageURL(code),
	}
	if fallback != nil {
		if out.Brand == "" {
			out.Brand = fallback.Brand
		}
		if out.Quantity == "" {
			out.Quantity = fallback.Quantity
		}
		if len(out.Allergens) == 0 {
			out.Allergens = fallback.Allergens
		}
		if len(out.Labels) == 0 {
			out.Labels = fallback.Labels
		}
		if out.Stores == "" {
			out.Stores = fallback.Stores
		}
		if out.ImageURL == "" {
			out.ImageURL = fallback.ImageURL
		}
		if out.NutriscoreGrade == "" {
			out.NutriscoreGrade = fallback.NutriscoreGrade
		}
	}
	return out, true
}

// offSearch queries the search index. ok=false means the source was
// unreachable (or returned garbage), so callers can set fallback.
func offSearch(query string, pageSize int) ([]map[string]any, bool) {
	u, err := url.Parse(offSearchURL)
	if err != nil {
		return nil, false
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("page_size", strconv.Itoa(pageSize))
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", offUserAgent)

	client := &http.Client{Timeout: offTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, false
	}
	var data struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, false
	}
	return data.Hits, true
}

type offDetailStatus string

const (
	offFound   offDetailStatus = "found"
	offMissing offDetailStatus = "missing"
	offError   offDetailStatus = "error"
)

func offProductDetail(code string) (map[string]any, offDetailStatus) {
	reqURL := offProductURL + "/" + url.PathEscape(code) + ".json?fields=" + url.QueryEscape(offProductFields)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, offError
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", offUserAgent)

	client := &http.Client{Timeout: offTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, offError
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, offMissing
	}
	if resp.StatusCode != http.StatusOK {
		return nil, offError
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, offError
	}
	var data struct {
		Status  int            `json:"status"`
		Product map[string]any `json:"product"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, offError
	}
	if data.Status == 1 && data.Product != nil {
		return data.Product, offFound
	}
	// OFF reports unknown codes as status 0.
	return nil, offMissing
}

// ---------------------------------------------------------------------------
// Search + lookup
// ---------------------------------------------------------------------------

type productSearchOptions struct {
	Restrictions []Restriction
	Blacklist    []string
	Limit        int
}

func searchProducts(query string, opts productSearchOptions) productSearchResponse {
	limit := opts.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 8 {
		limit = 8
	}

	hits, ok := offSearch(query, min(limit+3, 12))
	if !ok {
		return productSearchResponse{Query: query, Products: []EvaluatedProduct{}, Fallback: true}
	}

	candidates := make([]ProductCandidate, 0, limit)
	for _, hit := range hits {
		c, ok := mapSearchHit(hit)
		if !ok {
			continue
		}
		candidates = append(candidates, c)
		if len(candidates) >= limit {
			break
		}
	}
	if len(candidates) == 0 {
		return productSearchResponse{Query: query, Products: []EvaluatedProduct{}, Fallback: false}
	}

	products := make([]EvaluatedProduct, 0, len(candidates))
	for _, candidate := range candidates {
		merged := candidate
		if detail, status := offProductDetail(candidate.Code); status == offFound {
			if m, ok := mapProductDetail(detail, &candidate); ok {
				merged = m
			}
		}
		products = append(products, evaluateProductJSON(merged, opts.Restrictions, opts.Blacklist))
	}
	sortProducts(products)
	return productSearchResponse{Query: query, Products: products, Fallback: false}
}

func stripNonDigits(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normalizeBarcode normalizes a scanned/typed barcode to the canonical
// lookup form: UPC-A (12) is zero-padded to EAN-13, GTIN-14 drops a leading
// zero. ok=false when the digits can't be a GTIN (8/12/13/14 digits).
func normalizeBarcode(raw string) (string, bool) {
	digits := stripNonDigits(raw)
	switch len(digits) {
	case 8, 13:
		return digits, true
	case 12:
		return "0" + digits, true
	case 14:
		if digits[0] == '0' {
			return digits[1:], true
		}
		return digits, true
	}
	return "", false
}

// isValidBarcode is the GTIN mod-10 checksum (EAN-8 / EAN-13 / UPC-A / GTIN-14).
func isValidBarcode(raw string) bool {
	digits := stripNonDigits(raw)
	if len(digits) != 8 && len(digits) != 13 && len(digits) != 14 {
		return false
	}
	sum := 0
	for i := 0; i < len(digits)-1; i++ {
		weight := 1
		if (len(digits)-2-i)%2 == 0 {
			weight = 3
		}
		sum += int(digits[i]-'0') * weight
	}
	check := int(digits[len(digits)-1] - '0')
	return (10-(sum%10))%10 == check
}

// LookupBarcodeProduct looks up one packaged product by barcode and evaluates
// it against the user's restrictions — the scan-a-product path.
//
// The checksum decides how a miss is reported: a well-formed code that isn't
// in Open Food Facts is "not_found", while a checksum failure is
// "invalid_barcode" so the agent asks for the digits again instead of
// treating a misread as a real product. A code with a failing checksum that
// does exist is still returned (OFF contains non-GTIN internal codes).
func LookupBarcodeProduct(rawBarcode string, restrictions []Restriction, blacklist []string) BarcodeLookupResponse {
	digits := stripNonDigits(rawBarcode)
	barcode, ok := normalizeBarcode(digits)
	if !ok {
		return BarcodeLookupResponse{Barcode: digits, Status: "invalid_barcode"}
	}

	detail, status := offProductDetail(barcode)
	switch status {
	case offError:
		return BarcodeLookupResponse{Barcode: barcode, Status: "source_unavailable"}
	case offMissing:
		st := "not_found"
		if !isValidBarcode(barcode) {
			st = "invalid_barcode"
		}
		return BarcodeLookupResponse{Barcode: barcode, Status: st}
	}

	candidate, ok := mapProductDetail(detail, nil)
	if !ok {
		return BarcodeLookupResponse{Barcode: barcode, Status: "not_found"}
	}
	product := evaluateProductJSON(candidate, restrictions, blacklist)
	return BarcodeLookupResponse{Barcode: barcode, Status: "found", Product: &product}
}

// ---------------------------------------------------------------------------
// Tools
// ---------------------------------------------------------------------------

// findProductsArgs is the input schema for find_products.
type findProductsArgs struct {
	Query      string `json:"query" jsonschema_description:"Product or category to look for, e.g. 'dairy-free yogurt' or 'gluten-free pasta'"`
	MaxResults int    `json:"maxResults,omitempty" jsonschema:"minimum=1,maximum=8"`
}

// FindProducts searches Open Food Facts for real branded products and checks
// every candidate against the user's saved restrictions and blacklist.
func FindProducts(restrictions []Restriction, blacklist []string) *Tool {
	return &Tool{
		Name: "find_products",
		Description: "Find real branded grocery products (packaged foods you can buy) that fit the user's dietary restrictions. " +
			"Searches Open Food Facts for products by name or category, then checks each candidate against the user's saved restrictions and blacklist. " +
			"Every result carries a verdict: 'safe' (ingredient list and allergens scanned, no conflict), 'check' (e.g. 'may contain' traces or missing label data — tell the user to verify the packaging), or 'unsafe' (conflicts). " +
			"Results include the product's ingredient list, allergens and 'may contain' warnings. " +
			"Use this when the user asks which product or brand to buy, e.g. 'what dairy-free yogurt should I get?'. " +
			"Only present 'safe' products as recommendations; mention 'check' ones with the caveat attached, never suggest an 'unsafe' product.",
		Parameters: schemaFor(&findProductsArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in findProductsArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			out := searchProducts(in.Query, productSearchOptions{
				Restrictions: restrictions,
				Blacklist:    blacklist,
				Limit:        in.MaxResults,
			})
			return mustJSON(out), nil
		},
	}
}

// lookupProductByBarcodeArgs is the input schema for lookup_product_by_barcode.
type lookupProductByBarcodeArgs struct {
	Barcode string `json:"barcode" jsonschema_description:"Barcode digits, e.g. '4006381333931'. Spaces and dashes are ignored."`
}

// LookupProductByBarcode looks up one packaged product by barcode and checks
// it against the user's dietary restrictions.
func LookupProductByBarcode(restrictions []Restriction, blacklist []string) *Tool {
	return &Tool{
		Name: "lookup_product_by_barcode",
		Description: "Look up one packaged product by its barcode number (EAN-8/EAN-13/UPC-A/GTIN-14 — the digits printed under the bars) and check it against the user's dietary restrictions. " +
			"Use this when a photo shows a barcode, when the user reads out or types a barcode number, or when they ask about a specific packaged item they have in hand. " +
			"Returns the product with its ingredient list, allergens and the same fit verdict as find_products. " +
			"Never invent or guess barcode digits — if the printed digits are unreadable, ask for a clearer photo or the number. " +
			"Statuses: 'found' (product returned), 'not_found' (valid code, not in Open Food Facts — offer to check the ingredient label from a photo), 'invalid_barcode' (checksum failed — the digits were likely misread; ask again), 'source_unavailable' (data source unreachable).",
		Parameters: schemaFor(&lookupProductByBarcodeArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in lookupProductByBarcodeArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			out := LookupBarcodeProduct(in.Barcode, restrictions, blacklist)
			return mustJSON(out), nil
		},
	}
}
