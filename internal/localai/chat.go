package localai

import (
	"context"
	"fmt"
	"strings"

	"github.com/hybridgroup/yzma/pkg/llama"
	"github.com/orionryan/diet-app-go/internal/ai/provider"
)

// Chat runs a non-streaming chat completion against the active model.
//
// This is the text-only path. For vision models with attached images, see
// ChatVision in vision.go.
func (m *Manager) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	lm := m.Active()
	if lm == nil {
		return nil, fmt.Errorf("no local model loaded; visit /models to load one")
	}

	// Wait for manager-init to finish (InitLibs might be in progress).
	if err := m.InitLibs(); err != nil {
		return nil, err
	}

	lm.mu.Lock()
	defer lm.mu.Unlock()

	prompt := buildPrompt(lm, req)
	tokens := llama.Tokenize(lm.Vocab, prompt, true, true)

	nCtx := int(llama.NCtx(lm.Context))
	if len(tokens) > nCtx-512 {
		tokens = tokens[len(tokens)-(nCtx-512):]
	}

	llama.SamplerReset(lm.Sampler)
	mem, _ := llama.GetMemory(lm.Context)
	llama.MemoryClear(mem, true)

	batch := llama.BatchGetOne(tokens)
	if _, err := llama.Decode(lm.Context, batch); err != nil {
		return nil, fmt.Errorf("decode prompt: %w", err)
	}

	maxTokens := 2048
	generated := make([]llama.Token, 0, maxTokens)
	nPast := int32(len(tokens))

	for i := 0; i < maxTokens; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token := llama.SamplerSample(lm.Sampler, lm.Context, -1)
		llama.SamplerAccept(lm.Sampler, token)

		if llama.VocabIsEOG(lm.Vocab, token) {
			break
		}

		generated = append(generated, token)

		nextBatch := llama.BatchGetOne([]llama.Token{token})
		pos := llama.Pos(nPast)
		nextBatch.Pos = &pos
		nPast++

		if _, err := llama.Decode(lm.Context, nextBatch); err != nil {
			return nil, fmt.Errorf("decode token %d: %v", i, err)
		}
	}

	reply := llama.Detokenize(lm.Vocab, generated, false, false)

	var toolCalls []provider.ToolCall
	if len(req.Tools) > 0 {
		toolCalls, reply = provider.ParseToolCallsFromText(reply)
	}

	return &provider.ChatResponse{
		Choices: []provider.Choice{{
			Index: 0,
			Message: provider.Message{
				Role:      "assistant",
				Content:   reply,
				ToolCalls: toolCalls,
			},
		}},
	}, nil
}

// IsVision returns true if the active model is a vision-capable model.
func (m *Manager) IsVision() bool {
	lm := m.Active()
	return lm != nil && lm.IsVision
}

// Stream runs a streaming chat completion against the active model, invoking
// onDelta for every generated token. Tool calls are returned on the final
// chunk (parsed from the generated text, since local models emit tool calls as
// [[TOOL_CALL]] markup). onDelta is always called once with Done=true at the
// end (or Error set on failure).
func (m *Manager) Stream(ctx context.Context, req provider.ChatRequest, onDelta func(provider.StreamChunk)) error {
	lm := m.Active()
	if lm == nil {
		err := fmt.Errorf("no local model loaded; visit /models to load one")
		onDelta(provider.StreamChunk{Error: err.Error()})
		return err
	}
	if err := m.InitLibs(); err != nil {
		onDelta(provider.StreamChunk{Error: err.Error()})
		return err
	}

	lm.mu.Lock()
	defer lm.mu.Unlock()

	prompt := buildPrompt(lm, req)
	tokens := llama.Tokenize(lm.Vocab, prompt, true, true)

	nCtx := int(llama.NCtx(lm.Context))
	if len(tokens) > nCtx-512 {
		tokens = tokens[len(tokens)-(nCtx-512):]
	}

	llama.SamplerReset(lm.Sampler)
	mem, _ := llama.GetMemory(lm.Context)
	llama.MemoryClear(mem, true)

	batch := llama.BatchGetOne(tokens)
	if _, err := llama.Decode(lm.Context, batch); err != nil {
		e := fmt.Errorf("decode prompt: %w", err)
		onDelta(provider.StreamChunk{Error: e.Error()})
		return e
	}

	maxTokens := 2048
	generated := make([]llama.Token, 0, maxTokens)
	nPast := int32(len(tokens))

	for i := 0; i < maxTokens; i++ {
		if err := ctx.Err(); err != nil {
			onDelta(provider.StreamChunk{Error: err.Error()})
			return err
		}
		token := llama.SamplerSample(lm.Sampler, lm.Context, -1)
		llama.SamplerAccept(lm.Sampler, token)

		if llama.VocabIsEOG(lm.Vocab, token) {
			break
		}

		generated = append(generated, token)

		// Detokenize just this token for a streaming delta.
		delta := llama.Detokenize(lm.Vocab, []llama.Token{token}, false, false)
		if delta != "" {
			onDelta(provider.StreamChunk{Delta: delta})
		}

		nextBatch := llama.BatchGetOne([]llama.Token{token})
		pos := llama.Pos(nPast)
		nextBatch.Pos = &pos
		nPast++

		if _, err := llama.Decode(lm.Context, nextBatch); err != nil {
			e := fmt.Errorf("decode token %d: %v", i, err)
			onDelta(provider.StreamChunk{Error: e.Error()})
			return e
		}
	}

	reply := llama.Detokenize(lm.Vocab, generated, false, false)
		var toolCalls []provider.ToolCall
	if len(req.Tools) > 0 {
		toolCalls, _ = provider.ParseToolCallsFromText(reply)
	}
	onDelta(provider.StreamChunk{ToolCalls: toolCalls, Done: true})
	return nil
}

func buildPrompt(lm *LoadedModel, req provider.ChatRequest) string {
	if lm.ChatTmpl != "" {
		chatMsgs := make([]llama.ChatMessage, 0, len(req.Messages))
		for _, m := range req.Messages {
			chatMsgs = append(chatMsgs, llama.NewChatMessage(m.Role, m.Content))
		}
		buf := make([]byte, 64*1024)
		n := llama.ChatApplyTemplate(lm.ChatTmpl, chatMsgs, true, buf)
		if n > 0 {
			return string(buf[:n])
		}
	}

	var sb strings.Builder
	if len(req.Tools) > 0 {
		sb.WriteString("You have access to the following tools. To call a tool, output a JSON object wrapped in [[TOOL_CALL]] markers like this:\n\n")
		sb.WriteString(`[[TOOL_CALL]]{ "name": "tool_name", "arguments": { "key": "value" } }[[TOOL_CALL]]` + "\n\n")
		sb.WriteString("Available tools:\n\n")
		for _, t := range req.Tools {
			sb.WriteString(fmt.Sprintf("### %s\n%s\n\nParameters:\n%s\n\n",
				t.Function.Name, t.Function.Description, string(t.Function.Parameters)))
		}
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			sb.WriteString("<|im_start|>system\n")
			sb.WriteString(m.Content)
			sb.WriteString("<|im_end|>\n")
		case "user":
			sb.WriteString("<|im_start|>user\n")
			sb.WriteString(m.Content)
			sb.WriteString("<|im_end|>\n")
		case "assistant":
			sb.WriteString("<|im_start|>assistant\n")
			sb.WriteString(m.Content)
			sb.WriteString("<|im_end|>\n")
		case "tool":
			sb.WriteString("<|im_start|>tool\n")
			sb.WriteString(m.Content)
			sb.WriteString("<|im_end|>\n")
		}
	}
	sb.WriteString("<|im_start|>assistant\n")
	return sb.String()
}
