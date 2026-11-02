package tools

import (
	"context"
	"database/sql"
	"testing"
)

func setupActionsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`create table restrictions (
			id text primary key,
			user_id text not null,
			restriction text not null,
			severity text not null default 'avoid',
			created_at text,
			unique (user_id, restriction)
		)`,
		`create table blacklist (
			id text primary key,
			user_id text not null,
			ingredient text not null,
			created_at text,
			unique (user_id, ingredient)
		)`,
		`create table profile_targets (
			user_id text primary key,
			calorie_target integer,
			updated_at text
		)`,
		`create table grocery_lists (
			user_id text not null,
			week_start text not null,
			items_json text not null default '[]',
			updated_at text,
			primary key (user_id, week_start)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

func TestSetAndRemoveRestriction(t *testing.T) {
	db := setupActionsDB(t)
	uid := "u1"

	out := mustToolJSON(t, SetRestriction(db, uid),
		`{"restriction":"nut-free","severity":"anaphylaxis"}`)
	if out["error"] != nil {
		t.Fatalf("unexpected error: %v", out)
	}

	var severity string
	if err := db.QueryRow(
		`select severity from restrictions where user_id = ? and restriction = ?`,
		uid, "nut-free").Scan(&severity); err != nil {
		t.Fatal(err)
	}
	if severity != "anaphylaxis" {
		t.Fatalf("severity = %q, want anaphylaxis", severity)
	}

	// A second set updates the same row instead of failing on the unique key.
	mustToolJSON(t, SetRestriction(db, uid),
		`{"restriction":"nut-free","severity":"intolerance"}`)
	var count int
	if err := db.QueryRow(`select count(*) from restrictions where user_id = ?`, uid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("restriction rows = %d, want 1", count)
	}

	removed := mustToolJSON(t, RemoveRestriction(db, uid), `{"restriction":"nut-free"}`)
	if removed["error"] != nil {
		t.Fatalf("unexpected remove error: %v", removed)
	}
	if err := db.QueryRow(`select count(*) from restrictions where user_id = ?`, uid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("restriction rows = %d, want 0", count)
	}
}

func TestCalorieTargetRoundTrip(t *testing.T) {
	db := setupActionsDB(t)
	ctx := context.Background()
	uid := "u1"

	if target, err := LoadCalorieTarget(ctx, db, uid); err != nil || target != 0 {
		t.Fatalf("empty target = %d, %v; want 0, nil", target, err)
	}

	out := mustToolJSON(t, SetCalorieTarget(db, uid), `{"calories":2100}`)
	if out["error"] != nil {
		t.Fatalf("unexpected error: %v", out)
	}
	if target, err := LoadCalorieTarget(ctx, db, uid); err != nil || target != 2100 {
		t.Fatalf("target = %d, %v; want 2100, nil", target, err)
	}

	mustToolJSON(t, SetCalorieTarget(db, uid), `{"calories":0}`)
	if target, err := LoadCalorieTarget(ctx, db, uid); err != nil || target != 0 {
		t.Fatalf("cleared target = %d, %v; want 0, nil", target, err)
	}
}

func TestGroceryListToolFlow(t *testing.T) {
	db := setupActionsDB(t)
	uid := "u1"

	add := mustToolJSON(t, AddToGroceryList(db, uid),
		`{"items":[{"name":"Oat Milk","amount":1,"unit":"carton"},{"name":"spinach","amount":2,"unit":"bag"}]}`)
	if add["error"] != nil {
		t.Fatalf("unexpected add error: %v", add)
	}
	added, _ := add["added"].([]any)
	if len(added) != 2 {
		t.Fatalf("added = %v, want 2 items", added)
	}

	// Duplicate names merge and sum amounts.
	add = mustToolJSON(t, AddToGroceryList(db, uid),
		`{"items":[{"name":"oat milk","amount":2,"unit":"carton"}]}`)
	if add["error"] != nil {
		t.Fatalf("unexpected add error: %v", add)
	}
	items, err := LoadGroceryList(context.Background(), db, uid, currentWeekStart())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Name != "Oat Milk" || items[0].Amount != 3 {
		t.Fatalf("merged item = %+v, want Oat Milk x3", items[0])
	}

	check := mustToolJSON(t, CheckGroceryItem(db, uid),
		`{"names":["spinach"],"checked":true}`)
	if check["error"] != nil {
		t.Fatalf("unexpected check error: %v", check)
	}
	items, _ = LoadGroceryList(context.Background(), db, uid, currentWeekStart())
	if !items[1].Checked {
		t.Fatalf("spinach was not checked: %+v", items[1])
	}

	list := mustToolJSON(t, ListGroceryList(db, uid), `{}`)
	if list["error"] != nil {
		t.Fatalf("unexpected list error: %v", list)
	}

	remove := mustToolJSON(t, RemoveFromGroceryList(db, uid), `{"names":["oat milk"]}`)
	removed, _ := remove["removed"].([]any)
	if len(removed) != 1 {
		t.Fatalf("removed = %v, want 1 item", removed)
	}
	items, _ = LoadGroceryList(context.Background(), db, uid, currentWeekStart())
	if len(items) != 1 || items[0].Name != "spinach" {
		t.Fatalf("remaining = %+v, want spinach", items)
	}

	clear := mustToolJSON(t, ClearGroceryListTool(db, uid), `{}`)
	if clear["error"] != nil {
		t.Fatalf("unexpected clear error: %v", clear)
	}
	items, _ = LoadGroceryList(context.Background(), db, uid, currentWeekStart())
	if len(items) != 0 {
		t.Fatalf("items after clear = %d, want 0", len(items))
	}
}

func TestNavigateTo(t *testing.T) {
	out := mustToolJSON(t, NavigateTo(), `{"path":"/groceries"}`)
	if out["path"] != "/groceries" || out["navigate"] != true {
		t.Fatalf("navigate result = %v", out)
	}
	bad := mustToolJSON(t, NavigateTo(), `{"path":"/admin"}`)
	if bad["error"] == nil {
		t.Fatalf("expected an error for /admin: %v", bad)
	}
}

func TestGroupGroceryListOrdersCategories(t *testing.T) {
	groups := GroupGroceryList([]GroceryItem{
		{Name: "olive oil", Category: "Oils & Vinegars"},
		{Name: "spinach", Category: "Produce"},
		{Name: "apples", Category: "Produce"},
		{Name: "miso", Category: "Umami"},
	})
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(groups))
	}
	if groups[0].Category != "Produce" || groups[1].Category != "Oils & Vinegars" {
		t.Fatalf("category order = %v", groups)
	}
	if groups[2].Category != "Umami" {
		t.Fatalf("unknown category was dropped: %v", groups)
	}
	if groups[0].Items[0].Name != "apples" {
		t.Fatalf("items not sorted: %+v", groups[0].Items)
	}
}
