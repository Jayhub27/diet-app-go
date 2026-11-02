package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

type setRestrictionArgs struct {
	Restriction RestrictionName `json:"restriction"`
	Severity    Severity        `json:"severity"`
}

// SetRestriction adds or changes a saved dietary restriction.
func SetRestriction(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name: "set_restriction",
		Description: "Add or change a saved dietary restriction and its severity. " +
			"Use when the user says they are vegan, gluten-free, allergic to something, or wants the severity changed. " +
			"The restriction is enforced on every future recipe and product check.",
		Parameters: schemaFor(&setRestrictionArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			if db == nil || userID == "" {
				return mustJSON(map[string]any{"error": "Sign in to save restrictions."}), nil
			}
			var in setRestrictionArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			name := strings.TrimSpace(string(in.Restriction))
			if name == "" {
				return mustJSON(map[string]any{"error": "restriction is required"}), nil
			}
			severity := in.Severity
			if severity == "" {
				severity = SeverityAvoid
			}
			_, err := db.ExecContext(ctx,
				`insert into restrictions (id, user_id, restriction, severity) values (?, ?, ?, ?)
				 on conflict (user_id, restriction) do update set severity = excluded.severity`,
				uuid.NewString(), userID, name, string(severity))
			if err != nil {
				return mustJSON(map[string]any{"error": err.Error()}), nil
			}
			return mustJSON(map[string]any{
				"restriction": name, "severity": string(severity), "error": nil,
			}), nil
		},
	}
}

type removeRestrictionArgs struct {
	Restriction RestrictionName `json:"restriction"`
}

func RemoveRestriction(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name: "remove_restriction",
		Description: "Remove a saved dietary restriction. Only do this when the user explicitly asks; " +
			"never clear an anaphylaxis restriction without a direct confirmation in their message.",
		Parameters: schemaFor(&removeRestrictionArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in removeRestrictionArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			name := strings.TrimSpace(string(in.Restriction))
			_, err := db.ExecContext(ctx,
				`delete from restrictions where user_id = ? and restriction = ?`, userID, name)
			if err != nil {
				return mustJSON(map[string]any{"removed": name, "error": err.Error()}), nil
			}
			return mustJSON(map[string]any{"removed": name, "error": nil}), nil
		},
	}
}

type setCalorieTargetArgs struct {
	Calories int `json:"calories" jsonschema:"minimum=0,maximum=10000" jsonschema_description:"Daily kcal target; 0 clears it"`
}

func SetCalorieTarget(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "set_calorie_target",
		Description: "Set or clear the user's daily calorie target in kcal. Pass 0 to clear it.",
		Parameters:  schemaFor(&setCalorieTargetArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in setCalorieTargetArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			target := in.Calories
			if target < 0 {
				target = 0
			}
			if db == nil || userID == "" {
				return mustJSON(map[string]any{"calorieTarget": target, "error": "Sign in to save a target."}), nil
			}
			_, err := db.ExecContext(ctx,
				`insert into profile_targets (user_id, calorie_target, updated_at)
				 values (?, ?, datetime('now'))
				 on conflict (user_id) do update
				 set calorie_target = excluded.calorie_target, updated_at = datetime('now')`,
				userID, target)
			if err != nil {
				return mustJSON(map[string]any{"calorieTarget": target, "error": err.Error()}), nil
			}
			return mustJSON(map[string]any{"calorieTarget": target, "error": nil}), nil
		},
	}
}

// LoadCalorieTarget is shared by the profile page and the chat context.
func LoadCalorieTarget(ctx context.Context, db *sql.DB, userID string) (int, error) {
	if db == nil || userID == "" {
		return 0, nil
	}
	var target sql.NullInt64
	err := db.QueryRowContext(ctx,
		`select calorie_target from profile_targets where user_id = ?`, userID).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int(target.Int64), nil
}

var navigableRoutes = map[string]bool{
	"/":          true,
	"/chat":      true,
	"/groceries": true,
	"/pantry":    true,
	"/library":   true,
	"/profile":   true,
	"/scan":      true,
	"/models":    true,
}

type navigateArgs struct {
	Path string `json:"path" jsonschema_description:"One of /, /chat, /groceries, /pantry, /library, /profile, /scan, /models"`
}

// NavigateTo lets the agent open a screen, the same way the user taps a nav link.
func NavigateTo() *Tool {
	return &Tool{
		Name: "navigate_to",
		Description: "Open one of the app screens for the user. Use when they say 'show me my pantry', " +
			"'open my grocery list', or after a change they should see. Allowed: /, /chat, /groceries, /pantry, /library, /profile, /scan, /models.",
		Parameters: schemaFor(&navigateArgs{}),
		Run: func(_ context.Context, args json.RawMessage) (string, error) {
			var in navigateArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			if !navigableRoutes[in.Path] {
				return mustJSON(map[string]any{"error": "unknown path"}), nil
			}
			return mustJSON(map[string]any{"path": in.Path, "navigate": true, "error": nil}), nil
		},
	}
}
