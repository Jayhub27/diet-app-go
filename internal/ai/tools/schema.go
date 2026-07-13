package tools

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// reflector produces OpenAI-compatible tool parameter schemas from Go structs.
// DoNotReference + ExpandedStruct inline everything (no $defs/$ref), which every
// OpenAI-compatible provider (OpenAI, OpenRouter, Ollama, LM Studio) accepts.
var reflector = jsonschema.Reflector{
	DoNotReference: true,
	ExpandedStruct: true,
}

// schemaFor reflects a struct value into a JSON-schema map suitable for the
// Tool.Parameters field. It strips meta keys ($schema, $id, $defs) that some
// providers reject. Fields are required unless their json tag has ",omitempty".
// Descriptions come from the `jsonschema_description` tag (comma-safe);
// constraints (minimum, enum, ...) come from the `jsonschema` tag or a type's
// JSONSchema() method.
func schemaFor(v any) map[string]any {
	s := reflector.Reflect(v)
	b, _ := json.Marshal(s)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	delete(m, "$schema")
	delete(m, "$id")
	delete(m, "$defs")
	return m
}
