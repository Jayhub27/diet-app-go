package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// GroceryItem is one line on the shopping list. Stored as JSON in
// grocery_lists so the shape can evolve without a migration per field.
type GroceryItem struct {
	Name        string   `json:"name"`
	Amount      float64  `json:"amount"`
	Unit        string   `json:"unit"`
	Category    string   `json:"category"`
	Checked     bool     `json:"checked"`
	FromRecipes []string `json:"fromRecipes,omitempty"`
}

type GroceryCategory struct {
	Category string        `json:"category"`
	Items    []GroceryItem `json:"items"`
}

func currentWeekStart() string {
	now := time.Now()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	monday := now.AddDate(0, 0, -(weekday - 1))
	return monday.Format("2006-01-02")
}

// CurrentWeekStart exposes the list's week key to the page handlers.
func CurrentWeekStart() string { return currentWeekStart() }

// LoadGroceryList reads one user/week list. Missing rows are an empty list.
func LoadGroceryList(ctx context.Context, db *sql.DB, userID, weekStart string) ([]GroceryItem, error) {
	if db == nil || userID == "" {
		return nil, nil
	}
	var raw string
	err := db.QueryRowContext(ctx,
		`select items_json from grocery_lists where user_id = ? and week_start = ?`,
		userID, weekStart).Scan(&raw)
	if err == sql.ErrNoRows {
		return []GroceryItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	var items []GroceryItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []GroceryItem{}, nil
	}
	return items, nil
}

// SaveGroceryList upserts the whole week.
func SaveGroceryList(ctx context.Context, db *sql.DB, userID, weekStart string, items []GroceryItem) error {
	if db == nil || userID == "" {
		return nil
	}
	if items == nil {
		items = []GroceryItem{}
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`insert into grocery_lists (user_id, week_start, items_json, updated_at)
		 values (?, ?, ?, datetime('now'))
		 on conflict (user_id, week_start) do update
		 set items_json = excluded.items_json, updated_at = datetime('now')`,
		userID, weekStart, string(raw))
	return err
}

// AddGroceryItems deduplicates by ingredient name and sums amounts. source
// is recorded on new rows ("Added by agent" or "Added by you").
func AddGroceryItems(ctx context.Context, db *sql.DB, userID string, inputs []GroceryItem, source string) ([]GroceryItem, []string, error) {
	if source == "" {
		source = "Added"
	}
	weekStart := currentWeekStart()
	items, err := LoadGroceryList(ctx, db, userID, weekStart)
	if err != nil {
		return nil, nil, err
	}
	byKey := map[string]int{}
	for i, item := range items {
		byKey[normalizeIngredientName(item.Name)] = i
	}
	var added []string
	for _, input := range inputs {
		name := normalizeDisplayName(input.Name)
		if name == "" {
			continue
		}
		key := normalizeIngredientName(name)
		if idx, ok := byKey[key]; ok {
			items[idx].Amount += input.Amount
			added = append(added, items[idx].Name)
			continue
		}
		amount := input.Amount
		if amount <= 0 {
			amount = 1
		}
		unit := input.Unit
		if unit == "" {
			unit = "item"
		}
		items = append(items, GroceryItem{
			Name: name, Amount: amount, Unit: unit,
			Category: categorizeIngredient(name),
			Checked:  false, FromRecipes: []string{source},
		})
		byKey[key] = len(items) - 1
		added = append(added, name)
	}
	if err := SaveGroceryList(ctx, db, userID, weekStart, items); err != nil {
		return nil, nil, err
	}
	return items, added, nil
}

func normalizeDisplayName(name string) string {
	name = trimSpace(name)
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func RemoveGroceryItems(ctx context.Context, db *sql.DB, userID string, names []string) ([]string, error) {
	weekStart := currentWeekStart()
	items, err := LoadGroceryList(ctx, db, userID, weekStart)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[normalizeIngredientName(name)] = true
	}
	removed := []string{}
	remaining := make([]GroceryItem, 0, len(items))
	for _, item := range items {
		if wanted[normalizeIngredientName(item.Name)] {
			removed = append(removed, item.Name)
			continue
		}
		remaining = append(remaining, item)
	}
	if err := SaveGroceryList(ctx, db, userID, weekStart, remaining); err != nil {
		return nil, err
	}
	return removed, nil
}

func CheckGroceryItems(ctx context.Context, db *sql.DB, userID string, names []string, checked bool) ([]string, error) {
	weekStart := currentWeekStart()
	items, err := LoadGroceryList(ctx, db, userID, weekStart)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[normalizeIngredientName(name)] = true
	}
	updated := []string{}
	for i := range items {
		if wanted[normalizeIngredientName(items[i].Name)] {
			items[i].Checked = checked
			updated = append(updated, items[i].Name)
		}
	}
	if err := SaveGroceryList(ctx, db, userID, weekStart, items); err != nil {
		return nil, err
	}
	return updated, nil
}

func ClearGroceryList(ctx context.Context, db *sql.DB, userID string) error {
	return SaveGroceryList(ctx, db, userID, currentWeekStart(), nil)
}

// GroupGroceryList groups by category in the app's display order.
func GroupGroceryList(items []GroceryItem) []GroceryCategory {
	byCategory := map[string][]GroceryItem{}
	for _, item := range items {
		cat := item.Category
		if cat == "" {
			cat = categorizeIngredient(item.Name)
		}
		byCategory[cat] = append(byCategory[cat], item)
	}
	out := make([]GroceryCategory, 0, len(byCategory))
	seen := make(map[string]bool, len(categoryOrder))
	for _, category := range categoryOrder {
		seen[category] = true
		items, ok := byCategory[category]
		if !ok {
			continue
		}
		sortGroceryItems(items)
		out = append(out, GroceryCategory{Category: category, Items: items})
	}
	// Never drop an item because its stored category is outside the known
	// order (older data, or a category added by a future import).
	leftover := make([]string, 0)
	for category := range byCategory {
		if !seen[category] {
			leftover = append(leftover, category)
		}
	}
	sort.Strings(leftover)
	for _, category := range leftover {
		items := byCategory[category]
		sortGroceryItems(items)
		out = append(out, GroceryCategory{Category: category, Items: items})
	}
	return out
}

func sortGroceryItems(items []GroceryItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Name < items[j-1].Name; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// ---- Agent tools ----

type groceryInput struct {
	Name   string  `json:"name" jsonschema_description:"Item name, e.g. 'oat milk'"`
	Amount float64 `json:"amount,omitempty" jsonschema_description:"Quantity (default 1)"`
	Unit   string  `json:"unit,omitempty" jsonschema_description:"Unit such as bag, carton, can, g"`
}

type addGroceryArgs struct {
	Items []groceryInput `json:"items" jsonschema_description:"Items to add"`
}

func AddToGroceryList(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "add_to_grocery_list",
		Description: "Add one or more items to the current grocery/shopping list. Use when the user says 'add X to my shopping list' or 'put Y on the grocery list'. Deduplicates by name and sums amounts.",
		Parameters:  schemaFor(&addGroceryArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			if db == nil || userID == "" {
				return mustJSON(map[string]any{"added": []string{}, "error": "Sign in to use the grocery list."}), nil
			}
			var in addGroceryArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			inputs := make([]GroceryItem, 0, len(in.Items))
			for _, item := range in.Items {
				inputs = append(inputs, GroceryItem{Name: item.Name, Amount: item.Amount, Unit: item.Unit})
			}
			items, added, err := AddGroceryItems(ctx, db, userID, inputs, "Added by agent")
			if err != nil {
				return mustJSON(map[string]any{"added": []string{}, "error": err.Error()}), nil
			}
			return mustJSON(map[string]any{"added": added, "items": items, "weekStart": currentWeekStart(), "error": nil}), nil
		},
	}
}

type groceryNamesArgs struct {
	Names []string `json:"names" jsonschema_description:"Item names"`
}

func RemoveFromGroceryList(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "remove_from_grocery_list",
		Description: "Remove items from the current grocery/shopping list by name. Use when the user says 'take X off the list'.",
		Parameters:  schemaFor(&groceryNamesArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in groceryNamesArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			removed, err := RemoveGroceryItems(ctx, db, userID, in.Names)
			if err != nil {
				return mustJSON(map[string]any{"removed": []string{}, "error": err.Error()}), nil
			}
			return mustJSON(map[string]any{"removed": removed, "error": nil}), nil
		},
	}
}

type checkGroceryArgs struct {
	Names   []string `json:"names"`
	Checked bool     `json:"checked"`
}

func CheckGroceryItem(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "check_grocery_item",
		Description: "Mark one or more grocery list items as bought or not bought.",
		Parameters:  schemaFor(&checkGroceryArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in checkGroceryArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			updated, err := CheckGroceryItems(ctx, db, userID, in.Names, in.Checked)
			if err != nil {
				return mustJSON(map[string]any{"updated": []string{}, "error": err.Error()}), nil
			}
			return mustJSON(map[string]any{"updated": updated, "checked": in.Checked, "error": nil}), nil
		},
	}
}

func ClearGroceryListTool(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "clear_grocery_list",
		Description: "Remove every item from the current grocery/shopping list. Only use when the user explicitly asks to clear it.",
		Parameters:  schemaFor(&struct{}{}),
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if err := ClearGroceryList(ctx, db, userID); err != nil {
				return mustJSON(map[string]any{"cleared": false, "error": err.Error()}), nil
			}
			return mustJSON(map[string]any{"cleared": true, "error": nil}), nil
		},
	}
}

func ListGroceryList(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "list_grocery_list",
		Description: "List the current grocery/shopping list, including checked-off items. Use when the user asks what they still need to buy.",
		Parameters:  schemaFor(&struct{}{}),
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			items, err := LoadGroceryList(ctx, db, userID, currentWeekStart())
			if err != nil {
				return mustJSON(map[string]any{"items": []GroceryItem{}, "error": err.Error()}), nil
			}
			return mustJSON(map[string]any{"items": items, "weekStart": currentWeekStart(), "error": nil}), nil
		},
	}
}

// GroupCount is a tiny helper for templates/tests.
func GroupCount(items []GroceryItem) string {
	return fmt.Sprintf("%d item(s)", len(items))
}
