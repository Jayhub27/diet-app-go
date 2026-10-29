package handlers

import "testing"

func TestParseIngredientJSON(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		want  []string
	}{
		{
			name:  "clean JSON array",
			reply: `["wheat flour","sugar","palm oil"]`,
			want:  []string{"wheat flour", "sugar", "palm oil"},
		},
		{
			name:  "markdown fenced",
			reply: "```json\n[\"peanut oil\",\"salt\"]\n```",
			want:  []string{"peanut oil", "salt"},
		},
		{
			name:  "prose around JSON",
			reply: `Here are the ingredients I found: ["milk","soy lecithin"] Hope that helps!`,
			want:  []string{"milk", "soy lecithin"},
		},
		{
			name:  "uppercase normalized",
			reply: `["WHEAT FLOUR","Sugar"]`,
			want:  []string{"wheat flour", "sugar"},
		},
		{
			name:  "deduplicates",
			reply: `["salt","salt","SALT"]`,
			want:  []string{"salt"},
		},
		{
			name:  "fallback newline list",
			reply: "wheat flour\nsugar\npalm oil",
			want:  []string{"wheat flour", "sugar", "palm oil"},
		},
		{
			name:  "empty reply",
			reply: ``,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseIngredientJSON(tt.reply)
			if len(got) != len(tt.want) {
				t.Fatalf("parseIngredientJSON(%q) = %v, want %v", tt.reply, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseIngredientJSON(%q)[%d] = %q, want %q", tt.reply, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMatchBlacklistEntry(t *testing.T) {
	blacklist := []string{"peanuts", "shellfish", "cilantro"}

	tests := []struct {
		ingredient string
		wantMatch  string // "" means no conflict
	}{
		{"peanut oil", "peanuts"},      // blacklist contained in ingredient
		{"roasted peanuts", "peanuts"}, // exact-ish containment
		{"peanuts", "peanuts"},         // exact
		{"shrimp paste", "shellfish"},  // hmm — "shrimp" isn't "shellfish"; expect no match
		{"fresh cilantro", "cilantro"}, // containment
		{"CILANTRO", "cilantro"},       // case-insensitive
		{"wheat flour", ""},            // no conflict
		{"sugar", ""},                  // no conflict
		{"", ""},                       // empty input
	}

	for _, tt := range tests {
		got := matchBlacklistEntry(tt.ingredient, blacklist)
		if tt.ingredient == "shrimp paste" {
			// "shrimp paste" does NOT textually match "shellfish" — document behavior
			if got != "" {
				t.Errorf("matchBlacklistEntry(%q) = %q, want no match (substring matching can't catch category terms)", tt.ingredient, got)
			}
			continue
		}
		if got != tt.wantMatch {
			t.Errorf("matchBlacklistEntry(%q) = %q, want %q", tt.ingredient, got, tt.wantMatch)
		}
	}
}

func TestParseIngredientList(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "comma separated",
			in:   "wheat flour, sugar, palm oil, salt",
			want: []string{"wheat flour", "sugar", "palm oil", "salt"},
		},
		{
			name: "newlines and bullets",
			in:   "- peanuts\n- milk\n• soy",
			want: []string{"peanuts", "milk", "soy"},
		},
		{
			name: "ingredients prefix",
			in:   "Ingredients: oats, honey; almonds",
			want: []string{"oats", "honey", "almonds"},
		},
		{
			name: "commas inside parentheses",
			in:   "milk (2% milkfat, vitamin D), sugar",
			want: []string{"milk (2% milkfat, vitamin D)", "sugar"},
		},
		{
			name: "numbered lines",
			in:   "1. flour\n2) sugar\n3. salt",
			want: []string{"flour", "sugar", "salt"},
		},
		{
			name: "deduplicates case-insensitively",
			in:   "Salt, salt, SALT",
			want: []string{"Salt"},
		},
		{
			name: "empty",
			in:   "  \n ",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseIngredientList(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseIngredientList(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseIngredientList(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}
