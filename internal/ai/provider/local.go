package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/hybridgroup/yzma/pkg/download"
	"github.com/hybridgroup/yzma/pkg/llama"
)

const (
	defaultModelURL  = "https://huggingface.co/QuantFactory/SmolLM2-135M-GGUF/resolve/main/SmolLM2-135M.Q4_K_M.gguf"
	defaultModelName = "SmolLM2-135M.Q4_K_M.gguf"
)

type localState struct {
	model   llama.Model
	vocab   llama.Vocab
	ctx     llama.Context
	sampler llama.Sampler
	mu      sync.Mutex
	ready   bool
}

var (
	localOnce sync.Once
	local     localState
	localErr  error
)

func initLocal(modelPath string) error {
	localOnce.Do(func() {
		libPath, err := ensureLibs()
		if err != nil {
			localErr = err
			return
		}

		log.Printf("local: loading llama.cpp libs from %s", libPath)
		if err := llama.Load(libPath); err != nil {
			localErr = fmt.Errorf("local: load libs: %w", err)
			return
		}
		llama.LogSet(llama.LogSilent())
		llama.Init()

		if modelPath == "" {
			modelPath, err = ensureModel()
			if err != nil {
				localErr = err
				return
			}
		}

		log.Printf("local: loading model from %s", modelPath)
		params := llama.ModelDefaultParams()

		model, err := llama.ModelLoadFromFile(modelPath, params)
		if err != nil {
			localErr = fmt.Errorf("local: load model: %w", err)
			return
		}

		vocab := llama.ModelGetVocab(model)

		ctxParams := llama.ContextDefaultParams()
		ctxParams.NCtx = 4096
		ctxParams.NBatch = 512
		ctx, err := llama.InitFromModel(model, ctxParams)
		if err != nil {
			llama.ModelFree(model)
			localErr = fmt.Errorf("local: init context: %w", err)
			return
		}

		samplerParams := llama.DefaultSamplerParams()
		sampler := llama.NewSampler(model, llama.DefaultSamplers, samplerParams)

		local.model = model
		local.vocab = vocab
		local.ctx = ctx
		local.sampler = sampler
		local.ready = true
		log.Printf("local: ready (model=%s)", llama.ModelDesc(model))
	})
	return localErr
}

func ensureLibs() (string, error) {
	if libPath := os.Getenv("YZMA_LIB"); libPath != "" {
		if download.AlreadyInstalled(libPath) {
			return libPath, nil
		}
	}

	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".yzma", "libs"),
		"/usr/local/lib/llama.cpp",
		"./llama-libs",
	}
	for _, c := range candidates {
		if download.AlreadyInstalled(c) {
			return c, nil
		}
	}

	dest := filepath.Join(home, ".yzma", "libs")
	if err := os.MkdirAll(dest, 0755); err != nil {
		return "", fmt.Errorf("local: create lib dir: %w", err)
	}

	processor := "cpu"
	if ok, ver := download.HasCUDA(); ok {
		log.Printf("local: CUDA detected (version %s), using CUDA build", ver)
		processor = "cuda"
	} else if ok, ver := download.HasROCm(); ok {
		log.Printf("local: ROCm detected (version %s), using ROCm build", ver)
		processor = "rocm"
	}

	log.Printf("local: downloading llama.cpp libraries (arch=%s, os=%s, processor=%s)...", runtime.GOARCH, runtime.GOOS, processor)
	if err := download.Get(runtime.GOARCH, runtime.GOOS, processor, "latest", dest); err != nil {
		return "", fmt.Errorf("local: download libs: %w", err)
	}
	log.Printf("local: llama.cpp libraries installed to %s", dest)
	return dest, nil
}

func ensureModel() (string, error) {
	modelsDir := download.DefaultModelsDir()
	modelPath := filepath.Join(modelsDir, defaultModelName)

	if _, err := os.Stat(modelPath); err == nil {
		return modelPath, nil
	}

	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		return "", fmt.Errorf("local: create models dir: %w", err)
	}

	log.Printf("local: downloading default model to %s...", modelPath)
	if err := download.GetModel(defaultModelURL, modelPath); err != nil {
		return "", fmt.Errorf("local: download model: %w", err)
	}
	log.Printf("local: model downloaded to %s", modelPath)
	return modelPath, nil
}

func localChat(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	local.mu.Lock()
	defer local.mu.Unlock()

	if !local.ready {
		return nil, fmt.Errorf("local: model not loaded")
	}

	prompt := buildLocalPrompt(req)

	tokens := llama.Tokenize(local.vocab, prompt, true, true)

	nCtx := int(llama.NCtx(local.ctx))
	if len(tokens) > nCtx-512 {
		tokens = tokens[len(tokens)-(nCtx-512):]
	}

	llama.SamplerReset(local.sampler)

	mem, _ := llama.GetMemory(local.ctx)
	llama.MemoryClear(mem, true)

	batch := llama.BatchGetOne(tokens)
	if _, err := llama.Decode(local.ctx, batch); err != nil {
		return nil, fmt.Errorf("local: decode prompt: %w", err)
	}

	maxTokens := 2048
	generated := make([]llama.Token, 0, maxTokens)
	nPast := int32(len(tokens))

	for i := 0; i < maxTokens; i++ {
		token := llama.SamplerSample(local.sampler, local.ctx, -1)
		llama.SamplerAccept(local.sampler, token)

		if llama.VocabIsEOG(local.vocab, token) {
			break
		}

		generated = append(generated, token)

		nextBatch := llama.BatchGetOne([]llama.Token{token})
		pos := llama.Pos(nPast)
		nextBatch.Pos = &pos
		nPast++

		if _, err := llama.Decode(local.ctx, nextBatch); err != nil {
			return nil, fmt.Errorf("local: decode token %d: %w", i, err)
		}
	}

	reply := llama.Detokenize(local.vocab, generated, false, false)

	var toolCalls []ToolCall
	if len(req.Tools) > 0 {
		toolCalls, reply = parseToolCallsFromText(reply)
	}

	resp := &ChatResponse{}
	resp.Choices = append(resp.Choices, Choice{
		Index: 0,
		Message: Message{
			Role:      "assistant",
			Content:   reply,
			ToolCalls: toolCalls,
		},
	})
	return resp, nil
}

func buildLocalPrompt(req ChatRequest) string {
	tmpl := llama.ModelChatTemplate(local.model, "")
	if tmpl != "" {
		chatMsgs := make([]llama.ChatMessage, 0, len(req.Messages))
		for _, m := range req.Messages {
			chatMsgs = append(chatMsgs, llama.NewChatMessage(m.Role, m.Content))
		}
		buf := make([]byte, 32*1024)
		n := llama.ChatApplyTemplate(tmpl, chatMsgs, true, buf)
		if n > 0 {
			return string(buf[:n])
		}
	}

	var sb strings.Builder

	if len(req.Tools) > 0 {
		sb.WriteString("You have access to the following tools. To call a tool, output a JSON object wrapped in [[TOOL_CALL]] markers like this:\n\n")
		sb.WriteString("[[TOOL_CALL]]{ \"name\": \"tool_name\", \"arguments\": { \"key\": \"value\" } }[[TOOL_CALL]]\n\n")
		sb.WriteString("You can call multiple tools by outputting multiple [[TOOL_CALL]] blocks. If you do not need to use any tools, just respond normally.\n\n")
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

var toolCallRe = regexp.MustCompile(`(?s)\[\[TOOL_CALL\]\](\{.*?\})\[\[TOOL_CALL\]\]`)

func parseToolCallsFromText(text string) ([]ToolCall, string) {
	matches := toolCallRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil, text
	}

	var toolCalls []ToolCall
	var nonToolParts []string
	lastEnd := 0

	for _, match := range matches {
		idx := strings.Index(text[lastEnd:], match[0])
		if idx > 0 {
			nonToolParts = append(nonToolParts, text[lastEnd:lastEnd+idx])
		}
		lastEnd = lastEnd + idx + len(match[0])

		var parsed struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(match[1]), &parsed); err != nil {
			nonToolParts = append(nonToolParts, match[0])
			continue
		}

		toolCalls = append(toolCalls, ToolCall{
			ID:   fmt.Sprintf("call_%d", len(toolCalls)),
			Type: "function",
			Function: FunctionCall{
				Name:      parsed.Name,
				Arguments: string(parsed.Arguments),
			},
		})
	}

	if lastEnd < len(text) {
		nonToolParts = append(nonToolParts, text[lastEnd:])
	}

	cleaned := strings.TrimSpace(strings.Join(nonToolParts, ""))
	return toolCalls, cleaned
}
