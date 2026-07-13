package tools

import (
	"context"
	"encoding/json"
)

// Tool is a named callable the agent can invoke. Each tool exposes an
// OpenAI-compatible schema (Schema) plus a Run function that executes the
// call and returns a JSON-serializable result as a string.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Run         func(ctx context.Context, args json.RawMessage) (string, error)
}

// Schema returns the OpenAI-compatible function definition.
func (t *Tool) Schema() (name string, desc string, params map[string]any) {
	return t.Name, t.Description, t.Parameters
}
