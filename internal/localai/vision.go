package localai

import (
	"context"
	"fmt"
	"strings"

	"github.com/hybridgroup/yzma/pkg/llama"
	"github.com/hybridgroup/yzma/pkg/mtmd"
	"github.com/hybridgroup/yzma/pkg/utils"
	"github.com/orionryan/diet-app-go/internal/ai/provider"
)

// ChatVision runs inference using the active vision model, with one or more
// images attached to the most recent user turn. The final user message in
// req.Messages must contain the prompt; each imagePath becomes a <__media__>
// marker in front of that prompt.
func (m *Manager) ChatVision(ctx context.Context, req provider.ChatRequest, imagePaths []string) (*provider.ChatResponse, error) {
	lm := m.Active()
	if lm == nil {
		return nil, fmt.Errorf("no local model loaded; visit /models to load one")
	}
	if !lm.IsVision || lm.MtmdCtx == 0 {
		return nil, fmt.Errorf("active model is not vision-capable")
	}
	if err := m.InitLibs(); err != nil {
		return nil, err
	}
	if len(imagePaths) == 0 {
		// Nothing to do visually — fall back to text chat.
		return m.Chat(ctx, req)
	}

	lm.mu.Lock()
	defer lm.mu.Unlock()

	// Build a list of bitmaps from the file paths. Free them on every exit.
	bitmaps := make([]mtmd.Bitmap, 0, len(imagePaths))
	for _, p := range imagePaths {
		bw := mtmd.BitmapInitFromFile(lm.MtmdCtx, p, false)
		if bw.Bitmap == 0 {
			for _, b := range bitmaps {
				mtmd.BitmapFree(b)
			}
			return nil, fmt.Errorf("failed to load image: %s", p)
		}
		bitmaps = append(bitmaps, bw.Bitmap)
	}
	defer func() {
		for _, b := range bitmaps {
			mtmd.BitmapFree(b)
		}
	}()

	// Apply chat template if available; the user message gets the media
	// marker prepended.
	marker := mtmd.DefaultMarker() // e.g. "<__media__>"
	chatMsgs := make([]llama.ChatMessage, 0, len(req.Messages))
	for i, msg := range req.Messages {
		content := msg.Content
		if i == len(req.Messages)-1 && msg.Role == "user" {
			// Insert one marker per image at the start of the user turn.
			prefix := strings.Repeat(marker+"\n", len(bitmaps))
			content = prefix + content
		}
		chatMsgs = append(chatMsgs, llama.NewChatMessage(msg.Role, content))
	}

	var promptText string
	if lm.ChatTmpl != "" {
		buf := make([]byte, 64*1024)
		n := llama.ChatApplyTemplate(lm.ChatTmpl, chatMsgs, true, buf)
		if n > 0 {
			promptText = string(buf[:n])
		}
	}
	if promptText == "" {
		// Fallback: minimal handcrafted template with markers already in the
		// user content above.
		var sb strings.Builder
		for _, msg := range chatMsgs {
			sb.WriteString("<|im_start|>" + utils.BytePtrToString(msg.Role) + "\n")
			sb.WriteString(utils.BytePtrToString(msg.Content))
			sb.WriteString("<|im_end|>\n")
		}
		sb.WriteString("<|im_start|>assistant\n")
		promptText = sb.String()
	}

	input := mtmd.NewInputText(promptText, true, true)
	chunks := mtmd.InputChunksInit()
	defer mtmd.InputChunksFree(chunks)

	rc := mtmd.Tokenize(lm.MtmdCtx, chunks, input, bitmaps)
	if rc != 0 {
		return nil, fmt.Errorf("tokenize vision input failed (code=%d)", rc)
	}

	// Reset sampling state and KV cache.
	llama.SamplerReset(lm.Sampler)
	mem, _ := llama.GetMemory(lm.Context)
	llama.MemoryClear(mem, true)

	// Eval image+text chunks.
	var nPast llama.Pos
	if errRC := mtmd.HelperEvalChunks(lm.MtmdCtx, lm.Context, chunks, 0, 0, 512, true, &nPast); errRC != 0 {
		return nil, fmt.Errorf("eval vision chunks failed (code=%d)", errRC)
	}

	maxTokens := 2048
	generated := make([]llama.Token, 0, maxTokens)

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
			return nil, fmt.Errorf("decode vision token %d: %v", i, err)
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
