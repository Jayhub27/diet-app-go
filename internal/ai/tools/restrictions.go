package tools

import (
	"context"
	"encoding/json"
	"strings"
)

// RestrictionViolation is a single ingredient that broke a restriction.
type RestrictionViolation struct {
	Ingredient  string   `json:"ingredient"`
	Restriction string   `json:"restriction"`
	Severity    Severity `json:"severity"`
	Reason      string   `json:"reason"`
}

// RestrictionCheck is the result of checking ingredients against restrictions.
type RestrictionCheck struct {
	Restrictions     []string               `json:"restrictions"`
	AllClear         bool                   `json:"allClear"`
	Violations       []RestrictionViolation `json:"violations"`
	CleanIngredients []string               `json:"cleanIngredients"`
	MaxSeverity      Severity               `json:"maxSeverity"`
}

// forbidden maps each restriction to the keyword list that triggers it plus a
// human-readable reason. Matched case-insensitively against ingredient names.
// Coarse but deterministic and free; validate_ingredients_deep provides a
// second LLM pass that catches hidden allergens this engine misses.
var forbidden = map[string]struct {
	keywords []string
	reason   string
}{
	"vegan": {
		keywords: []string{"chicken", "beef", "pork", "turkey", "lamb", "duck", "bacon", "ham", "sausage", "meat", "fish", "salmon", "tuna", "shrimp", "prawn", "cod", "anchovy", "seafood", "gelatin", "honey", "egg", "eggs", "milk", "cream", "butter", "cheese", "yogurt", "whey", "casein", "lard", "stock"},
		reason:   "not vegan (animal-derived)",
	},
	"vegetarian": {
		keywords: []string{"chicken", "beef", "pork", "turkey", "lamb", "duck", "bacon", "ham", "sausage", "meat", "fish", "salmon", "tuna", "shrimp", "prawn", "cod", "anchovy", "seafood", "gelatin", "lard", "stock", "broth"},
		reason:   "not vegetarian (meat/fish/gelatin)",
	},
	"pescatarian": {
		keywords: []string{"chicken", "beef", "pork", "turkey", "lamb", "duck", "bacon", "ham", "sausage", "meat", "gelatin", "lard"},
		reason:   "not pescatarian (meat/gelatin)",
	},
	"keto": {
		keywords: []string{"sugar", "flour", "rice", "bread", "pasta", "noodle", "potato", "oats", "quinoa", "corn", "couscous", "tortilla", "cracker", "cereal", "honey", "maple syrup", "agave", "banana", "mango"},
		reason:   "too high in carbs for keto",
	},
	"gluten-free": {
		keywords: []string{"wheat", "barley", "rye", "flour", "bread", "pasta", "noodle", "couscous", "soy sauce", "bulgur", "spelt", "semolina", "pita", "tortilla", "cracker", "cereal", "beer", "malt"},
		reason:   "contains gluten",
	},
	"dairy-free": {
		keywords: []string{"milk", "cream", "butter", "cheese", "yogurt", "whey", "casein", "lactose", "ghee"},
		reason:   "contains dairy",
	},
	"nut-free": {
		keywords: []string{"almond", "peanut", "walnut", "cashew", "pecan", "hazelnut", "pistachio", "macadamia", "brazil nut", "pine nut", "pecans", "nuts"},
		reason:   "contains nuts",
	},
	"low-sodium": {
		keywords: []string{"soy sauce", "salt", "bouillon", "msg", "bacon", "ham", "sausage", "cured", "deli meat", "pickle", "olives", "anchovy", "miso"},
		reason:   "high in sodium",
	},
	"halal": {
		keywords: []string{"pork", "bacon", "ham", "gelatin", "alcohol", "wine", "beer", "rum", "vodka", "liqueur", "lard"},
		reason:   "not halal (pork/alcohol/uncertified gelatin)",
	},
	"kosher": {
		keywords: []string{"pork", "bacon", "ham", "shellfish", "shrimp", "lobster", "crab", "clam", "mussel", "oyster", "gelatin"},
		reason:   "not kosher (pork/shellfish/uncertified gelatin)",
	},
	"low-fodmap": {
		keywords: []string{"onion", "garlic", "wheat", "rye", "apple", "pear", "watermelon", "cauliflower", "mushroom", "honey", "milk", "ice cream", "yogurt", "cashew", "pistachio", "beans", "lentils", "chickpea"},
		reason:   "high in FODMAPs",
	},
}

var severityRank = map[Severity]int{ //nolint:gochecknoglobals
	SeverityAvoid:       1,
	SeverityIntolerance: 2,
	SeverityAnaphylaxis: 3,
}

// applyRestrictions is the keyword engine used by the apply_restrictions tool
// and as the fallback for validate_ingredients_deep.
func applyRestrictions(ingredients []string, restrictions []Restriction) RestrictionCheck {
	violations := make([]RestrictionViolation, 0)
	flagged := make(map[int]bool, len(ingredients))
	for i, ing := range ingredients {
		low := strings.ToLower(ing)
		for _, r := range restrictions {
			rule, ok := forbidden[strings.ToLower(r.Name)]
			if !ok {
				continue
			}
			for _, kw := range rule.keywords {
				if strings.Contains(low, kw) {
					violations = append(violations, RestrictionViolation{
						Ingredient:  ing,
						Restriction: r.Name,
						Severity:    r.Severity,
						Reason:      rule.reason,
					})
					flagged[i] = true
					break
				}
			}
		}
	}
	clean := make([]string, 0, len(ingredients))
	for i, ing := range ingredients {
		if !flagged[i] {
			clean = append(clean, ing)
		}
	}
	var maxS Severity
	for _, v := range violations {
		if maxS == "" || severityRank[v.Severity] > severityRank[maxS] {
			maxS = v.Severity
		}
	}
	names := make([]string, 0, len(restrictions))
	for _, r := range restrictions {
		names = append(names, r.Name)
	}
	return RestrictionCheck{
		Restrictions:     names,
		AllClear:         len(violations) == 0,
		Violations:       violations,
		CleanIngredients: clean,
		MaxSeverity:      maxS,
	}
}

// applyRestrictionsArgs is the input schema for apply_restrictions.
type applyRestrictionsArgs struct {
	Ingredients  []string           `json:"ingredients"`
	Restrictions []restrictionInput `json:"restrictions"`
}

// ApplyRestrictions checks a list of ingredient names against one or more
// dietary restrictions with severity. Blacklist is handled via the agent's
// system prompt context, not as a tool parameter.
func ApplyRestrictions(rs []Restriction) *Tool {
	return &Tool{
		Name:        "apply_restrictions",
		Description: "Check a list of ingredient names against one or more dietary restrictions (vegan, vegetarian, pescatarian, keto, gluten-free, dairy-free, nut-free, low-sodium, halal, kosher, low-fodmap). Each restriction can have a severity: 'avoid' (standard), 'intolerance' (check hidden sources), or 'anaphylaxis' (life-threatening, refuse even cross-contamination). Returns any violations with severity, and the subset of ingredients that are safe. Always call this before finalising a recipe when the user named a restriction.",
		Parameters:  schemaFor(&applyRestrictionsArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in applyRestrictionsArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			// If the model did not pass restrictions, fall back to the user's
			// saved restrictions supplied at tool-construction time (rs).
			check := toRestrictions(in.Restrictions)
			if len(check) == 0 {
				check = rs
			}
			out := applyRestrictions(in.Ingredients, check)
			b, _ := json.Marshal(out)
			return string(b), nil
		},
	}
}
