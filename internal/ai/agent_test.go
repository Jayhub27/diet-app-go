package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/orionryan/diet-app-go/internal/ai/provider"
	"github.com/orionryan/diet-app-go/internal/ai/tools"
)

func testTool(name string, run func(context.Context, json.RawMessage) (string, error)) *tools.Tool {
	return &tools.Tool{
		Name:        name,
		Description: name,
		Parameters:  map[string]any{"type": "object"},
		Run:         run,
	}
}

func toolCall(id, name, args string) provider.Message {
	return provider.Message{
		Role: "assistant",
		ToolCalls: []provider.ToolCall{{
			ID:       id,
			Type:     "function",
			Function: provider.FunctionCall{Name: name, Arguments: args},
		}},
	}
}

func TestProfileChangeBlocksRecipesForTheRestOfTheRun(t *testing.T) {
	recipeRan := false
	byName := map[string]*tools.Tool{
		"set_restriction": testTool("set_restriction", func(context.Context, json.RawMessage) (string, error) {
			return `{"restriction":"vegan","severity":"avoid","error":null}`, nil
		}),
		"generate_recipe": testTool("generate_recipe", func(context.Context, json.RawMessage) (string, error) {
			recipeRan = true
			return `{"title":"should not run"}`, nil
		}),
	}

	res := &Result{ToolHit: map[string]int{}}
	var msgs []provider.Message

	// First turn: a profile mutation runs normally.
	if err := executeToolCalls(context.Background(), &Agent{}, byName,
		toolCall("1", "set_restriction", `{"restriction":"vegan","severity":"avoid"}`),
		RunOptions{}, res, &msgs); err != nil {
		t.Fatal(err)
	}
	if !res.ProfileChanged {
		t.Fatal("ProfileChanged was not set after a restriction change")
	}

	// Second turn: recipe generation must be refused in this same run.
	if err := executeToolCalls(context.Background(), &Agent{}, byName,
		toolCall("2", "generate_recipe", `{"title":"x"}`),
		RunOptions{}, res, &msgs); err != nil {
		t.Fatal(err)
	}
	if recipeRan {
		t.Fatal("generate_recipe ran after the profile changed")
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "profile changed") {
		t.Fatalf("expected a profile guard message, got %q", last.Content)
	}
}

func TestOnToolResultReceivesSuccessfulOutput(t *testing.T) {
	byName := map[string]*tools.Tool{
		"navigate_to": testTool("navigate_to", func(context.Context, json.RawMessage) (string, error) {
			return `{"path":"/groceries","navigate":true}`, nil
		}),
	}
	res := &Result{ToolHit: map[string]int{}}
	var msgs []provider.Message
	var got string

	if err := executeToolCalls(context.Background(), &Agent{}, byName,
		toolCall("1", "navigate_to", `{"path":"/groceries"}`),
		RunOptions{OnToolResult: func(name, output string) {
			got = name + " " + output
		}}, res, &msgs); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "navigate_to ") {
		t.Fatalf("OnToolResult not called: %q", got)
	}
	if !strings.Contains(got, `"/groceries"`) {
		t.Fatalf("OnToolResult output = %q", got)
	}
}
