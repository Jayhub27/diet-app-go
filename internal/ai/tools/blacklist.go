package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// addToBlacklistArgs is the input schema for add_to_blacklist.
type addToBlacklistArgs struct {
	Ingredients []string `json:"ingredients" jsonschema_description:"Ingredient names the user cannot or will not eat"`
}

// AddToBlacklist adds one or more ingredients to the user's blacklist.
// The blacklist is permanent and applied to every future recipe suggestion.
func AddToBlacklist(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "add_to_blacklist",
		Description: "Add one or more ingredients to the user's blacklist (things they cannot or will not eat). Use this when the user describes allergies, intolerances, or foods they dislike — extract concrete ingredient names and add them. Each entry is stored permanently and applied to every future recipe suggestion.",
		Parameters:  schemaFor(&addToBlacklistArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			if db == nil || userID == "" {
				return mustJSON(map[string]any{"added": []any{}, "error": "Sign in to use the blacklist feature."}), nil
			}
			var in addToBlacklistArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			added := make([]string, 0, len(in.Ingredients))
			for _, name := range in.Ingredients {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				_, err := db.ExecContext(ctx,
					`insert into blacklist (id, user_id, ingredient) values (?, ?, ?)`,
					uuid.NewString(), userID, name)
				if err != nil {
					return mustJSON(map[string]any{"added": added, "error": err.Error()}), nil
				}
				added = append(added, name)
			}
			return mustJSON(map[string]any{"added": added, "error": nil}), nil
		},
	}
}

// removeFromBlacklistArgs is the input schema for remove_from_blacklist.
type removeFromBlacklistArgs struct {
	Names []string `json:"names" jsonschema_description:"Ingredient names to remove from the blacklist"`
}

// RemoveFromBlacklist removes ingredients from the user's blacklist by name.
func RemoveFromBlacklist(db *sql.DB, userID string) *Tool {
	return &Tool{
		Name:        "remove_from_blacklist",
		Description: "Remove an ingredient from the user's blacklist by name. Use this when the user says 'I'm okay with X now', 'actually I can eat Y', or 'remove Z from my blacklist'. Matches by ingredient name (case-insensitive).",
		Parameters:  schemaFor(&removeFromBlacklistArgs{}),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			if db == nil || userID == "" {
				return mustJSON(map[string]any{"removed": []any{}, "error": "Sign in to use the blacklist feature."}), nil
			}
			var in removeFromBlacklistArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			rows, err := db.QueryContext(ctx,
				`select id, ingredient from blacklist where user_id = ?`, userID)
			if err != nil {
				return mustJSON(map[string]any{"removed": []any{}, "error": err.Error()}), nil
			}
			owned := map[string]string{}
			for rows.Next() {
				var id, name string
				if err := rows.Scan(&id, &name); err != nil {
					rows.Close()
					return mustJSON(map[string]any{"removed": []any{}, "error": err.Error()}), nil
				}
				owned[name] = id
			}
			rows.Close()

			removed := make([]string, 0)
			for _, name := range in.Names {
				for stored, id := range owned {
					if strings.EqualFold(stored, name) {
						if _, err := db.ExecContext(ctx, `delete from blacklist where id = ?`, id); err != nil {
							return mustJSON(map[string]any{"removed": removed, "error": err.Error()}), nil
						}
						removed = append(removed, stored)
						break
					}
				}
			}
			return mustJSON(map[string]any{"removed": removed, "error": nil}), nil
		},
	}
}