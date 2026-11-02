package handlers

import (
	"context"
	"database/sql"
	"testing"

	"github.com/orionryan/diet-app-go/internal/ai/tools"
	_ "modernc.org/sqlite"
)

func TestNavigationPath(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		output string
		want   string
	}{
		{"navigate", "navigate_to", `{"path":"/groceries","navigate":true}`, "/groceries"},
		{"other tool", "add_to_pantry", `{"path":"/groceries","navigate":true}`, ""},
		{"not navigable", "navigate_to", `{"path":"/groceries"}`, ""},
		{"bad json", "navigate_to", `not json`, ""},
		{"error result", "navigate_to", `{"error":"unknown path"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := navigationPath(tc.tool, tc.output); got != tc.want {
				t.Fatalf("navigationPath(%q, %q) = %q, want %q", tc.tool, tc.output, got, tc.want)
			}
		})
	}
}

func TestBuildGroceryView(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`create table grocery_lists (
		user_id text not null,
		week_start text not null,
		items_json text not null default '[]',
		updated_at text,
		primary key (user_id, week_start)
	)`)
	if err != nil {
		t.Fatal(err)
	}

	h := New(Deps{DB: db})
	uid := "u1"
	ctx := context.Background()
	if _, _, err := tools.AddGroceryItems(ctx, db, uid, []tools.GroceryItem{
		{Name: "spinach", Amount: 1, Unit: "bag"},
		{Name: "oat milk", Amount: 2, Unit: "carton"},
	}, "Added by you"); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.CheckGroceryItems(ctx, db, uid, []string{"spinach"}, true); err != nil {
		t.Fatal(err)
	}

	view, err := h.buildGroceryView(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 2 || view.Checked != 1 || view.Remaining != 1 {
		t.Fatalf("counts = total %d, checked %d, remaining %d; want 2/1/1",
			view.Total, view.Checked, view.Remaining)
	}
	if len(view.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(view.Groups))
	}
	if view.WeekStart != tools.CurrentWeekStart() {
		t.Fatalf("week start = %q", view.WeekStart)
	}
}
