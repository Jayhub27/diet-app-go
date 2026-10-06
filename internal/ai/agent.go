package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/orionryan/diet-app-go/internal/ai/provider"
	"github.com/orionryan/diet-app-go/internal/ai/tools"
)

// Agent runs a tool-calling loop with a single LLM provider. It is a direct
// port of the responsibilities of the TypeScript ToolLoopAgent: build
// messages, call the model, parse tool_calls, execute, append, repeat —
// up to MaxSteps.
type Agent struct {
	Client   *provider.Client
	Model    string
	Tools    []*tools.Tool
	MaxSteps int
}

type RunOptions struct {
	// System prompt (concatenated with any context block).
	System string
	// Conversation history (user/assistant/tool messages). The last message
	// should be the user's latest turn.
	Messages []provider.Message
	// Optional tool-level context for tools that need it.
	Pantry       []map[string]any
	Restrictions []tools.Restriction
	Blacklist    []string
	// OnToolCall is invoked once per tool execution (for SSE tool-status
	// cards, logging, etc.). nil = ignore.
	OnToolCall func(name string, args string)
	// OnToken is called for every assistant content delta in a streaming
	// response. nil = no streaming (the agent still works without it).
	// MVP: not used; agent runs non-streaming and returns the full reply.
	OnToken func(delta string)
}

// Result is what the agent returns after the loop.
type Result struct {
	Reply   string         // final assistant text
	Steps   int            // how many LLM calls were made
	Trace   []Step         // for debugging / UI
	ToolHit map[string]int // how many times each tool was called
}

type Step struct {
	Kind     string // "assistant" or "tool"
	Text     string
	ToolName string
	ToolArgs string
	ToolOut  string
	ToolErr  string
}

func (a *Agent) Run(ctx context.Context, opts RunOptions) (*Result, error) {
	if a.MaxSteps <= 0 {
		a.MaxSteps = 6
	}
	msgs := make([]provider.Message, 0, len(opts.Messages)+1)
	if opts.System != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: opts.System})
	}
	msgs = append(msgs, opts.Messages...)

	schemas := buildSchemas(a.Tools)
	byName := make(map[string]*tools.Tool, len(a.Tools))
	for _, t := range a.Tools {
		byName[t.Name] = t
	}

	res := &Result{ToolHit: make(map[string]int)}
	for step := 0; step < a.MaxSteps; step++ {
		req := provider.ChatRequest{
			Model:    a.Model,
			Messages: msgs,
			Tools:    schemas,
		}

		// Non-streaming path (default).
		if opts.OnToken == nil {
			resp, err := a.Client.Chat(ctx, req)
			if err != nil {
				return res, fmt.Errorf("llm call: %w", err)
			}
			if resp.Error != nil {
				return res, fmt.Errorf("llm error: %s", resp.Error.Message)
			}
			if len(resp.Choices) == 0 {
				return res, fmt.Errorf("llm returned no choices")
			}
			choice := resp.Choices[0].Message
			res.Steps++
			if len(choice.ToolCalls) == 0 {
				res.Reply = choice.Content
				res.Trace = append(res.Trace, Step{Kind: "assistant", Text: choice.Content})
				return res, nil
			}
			msgs = append(msgs, choice)
			res.Trace = append(res.Trace, Step{Kind: "assistant", Text: choice.Content})
			if err := executeToolCalls(ctx, a, byName, choice, opts, res, &msgs); err != nil {
				return res, err
			}
			continue
		}

		// Streaming path: stream tokens, accumulate the full reply.
		var streamed strings.Builder
		var finalToolCalls []provider.ToolCall
		streamErr := a.Client.ChatStream(ctx, req, func(chunk provider.StreamChunk) {
			if chunk.Error != "" {
				return
			}
			if chunk.Delta != "" {
				streamed.WriteString(chunk.Delta)
				opts.OnToken(chunk.Delta)
			}
			if chunk.Done {
				finalToolCalls = chunk.ToolCalls
			}
		})
		if streamErr != nil {
			return res, fmt.Errorf("llm call: %w", streamErr)
		}
		res.Steps++
		full := streamed.String()

		// OpenAI-style streamed tool calls (final chunk carries them).
		if len(finalToolCalls) > 0 {
			choice := provider.Message{Role: "assistant", Content: full, ToolCalls: finalToolCalls}
			msgs = append(msgs, choice)
			res.Trace = append(res.Trace, Step{Kind: "assistant", Text: full})
			if err := executeToolCalls(ctx, a, byName, choice, opts, res, &msgs); err != nil {
				return res, err
			}
			continue
		}

		// Local models emit tool calls as [[TOOL_CALL]] markup inside the text.
		if len(req.Tools) > 0 {
			if parsed, cleaned := provider.ParseToolCallsFromText(full); len(parsed) > 0 {
				choice := provider.Message{Role: "assistant", Content: cleaned, ToolCalls: parsed}
				msgs = append(msgs, choice)
				res.Trace = append(res.Trace, Step{Kind: "assistant", Text: cleaned})
				if err := executeToolCalls(ctx, a, byName, choice, opts, res, &msgs); err != nil {
					return res, err
				}
				continue
			}
		}

		// No tool calls → final reply.
		res.Reply = full
		res.Trace = append(res.Trace, Step{Kind: "assistant", Text: full})
		return res, nil
	}

	// Hit the step cap; surface what we have.
	res.Reply = "I ran out of steps before I could finish. Try a simpler request or a faster model."
	return res, nil
}

// executeToolCalls runs every tool call on the assistant message, appends the
// results as "tool" messages, and records them in the trace. It mutates msgs.
func executeToolCalls(ctx context.Context, a *Agent, byName map[string]*tools.Tool,
	choice provider.Message, opts RunOptions, res *Result, msgs *[]provider.Message) error {

	// Append assistant message with tool_calls (so the model sees its own
	// call in the next turn).
	*msgs = append(*msgs, choice)
	res.Trace = append(res.Trace, Step{Kind: "assistant", Text: choice.Content})

	for _, tc := range choice.ToolCalls {
		name := tc.Function.Name
		args := tc.Function.Arguments
		t, ok := byName[name]
		step := Step{Kind: "tool", ToolName: name, ToolArgs: args}
		res.ToolHit[name]++
		if !ok {
			step.ToolErr = fmt.Sprintf("unknown tool: %s", name)
			step.ToolOut = step.ToolErr
		} else {
			if opts.OnToolCall != nil {
				opts.OnToolCall(name, args)
			}
			out, err := t.Run(ctx, json.RawMessage(args))
			if err != nil {
				step.ToolErr = err.Error()
				step.ToolOut = err.Error()
			} else {
				step.ToolOut = out
			}
		}
		res.Trace = append(res.Trace, step)
		*msgs = append(*msgs, provider.Message{
			Role:       "tool",
			ToolCallID: tc.ID,
			Name:       name,
			Content:    step.ToolOut,
		})
	}
	return nil
}

func buildSchemas(ts []*tools.Tool) []provider.Tool {
	out := make([]provider.Tool, 0, len(ts))
	for _, t := range ts {
		name, desc, params := t.Schema()
		var pt provider.Tool
		pt.Type = "function"
		pt.Function.Name = name
		pt.Function.Description = desc
		b, _ := json.Marshal(params)
		pt.Function.Parameters = b
		out = append(out, pt)
	}
	return out
}

// SystemPrompt is the long-form instructions. We trim the prompt from the
// TS version and rely on tool descriptions for the how.
const SystemPrompt = `You are DietApp, an expert chef and nutritionist assistant.

Help the user create recipes that respect their dietary restrictions, calorie
targets, and pantry constraints.

Key rules:
1. Apply the user's saved dietary restrictions and blacklist automatically.
   If a restriction is named or in their profile, call apply_restrictions first.
   For anaphylaxis-level restrictions, also call validate_ingredients_deep.
2. Use compute_nutrition to verify calorie/macro targets before finalising.
3. Use search_ingredients to look up nutrient values when needed.
4. Use search_locations when the user asks where to buy ingredients.
5. Always finalise recipes by calling generate_recipe with full structured details.
6. For meal plans, call plan_week. Then call build_grocery_list for shopping.
7. Never invent nutrition numbers.
8. When the user's pantry has ingredients, prefer using them.
9. NEVER include blacklisted ingredients. Swap with alternatives.
10. Use common units (g, ml, cup, tbsp, tsp, piece, can, handful).
11. Be concise. Lead with the result, not the reasoning.
12. When unsure if an ingredient is allowed, ask instead of guessing.

Severity:
- 'avoid': don't include this category.
- 'intolerance': check hidden sources and derived ingredients.
- 'anaphylaxis': life-threatening — refuse even trace/cross-contamination.
  Always call validate_ingredients_deep.

Pantry tools:
- add_to_pantry: when user says "add X to my pantry" or "I bought Y"
- remove_from_pantry: when user says "remove X" or "I used up Y"
- list_pantry: when user asks "what's in my pantry?"
- save_recipe_to_library: when user says "save this recipe"
- Do NOT modify dietary profile or restrictions — that's manual on /profile.

Blacklist tools:
- add_to_blacklist: when the user describes allergies, intolerances, or foods
  they can't or won't eat. Extract concrete ingredient names and add them.
- remove_from_blacklist: when the user says they can now eat something or wants
  it off the blacklist.`

// BuildContextBlock appends a human-readable context block to the system
// prompt. Mirrors the TS buildContextBlock helper.
func BuildContextBlock(pantry []map[string]any, restrictions []tools.Restriction, blacklist []string) string {
	var parts []string
	if len(pantry) > 0 {
		lines := make([]string, 0, len(pantry))
		for _, p := range pantry {
			lines = append(lines, fmt.Sprintf("- %v", p["name"]))
		}
		parts = append(parts, "Pantry:\n"+strings.Join(lines, "\n"))
	}
	if len(restrictions) > 0 {
		lines := make([]string, 0, len(restrictions))
		for _, r := range restrictions {
			lines = append(lines, fmt.Sprintf("- %s (severity: %s)", r.Name, r.Severity))
		}
		parts = append(parts, "Restrictions:\n"+strings.Join(lines, "\n"))
	}
	if len(blacklist) > 0 {
		parts = append(parts, "Blacklist (never include):\n- "+strings.Join(blacklist, "\n- "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n\n--- User context ---\n" + strings.Join(parts, "\n")
}
