package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Source is one of the supported AI backends. All backends are reached via
// the OpenAI-compatible Chat Completions API (Ollama and LM Studio also
// implement it; Anthropic is reached via an OpenAI-compatible shim or
// native — out of scope for MVP).
type Source string

const (
	SourceOpenAI     Source = "openai"
	SourceOpenRouter Source = "openrouter"
	SourceOllama     Source = "ollama"
	SourceLMStudio   Source = "lmstudio"
	SourceLocal      Source = "local"
)

type Options struct {
	Source      Source
	Model       string
	APIKey      string
	BaseURL     string // for openai-compatible proxies / ollama / lmstudio
	AppURL      string // openrouter attribution
	AppName     string // openrouter attribution
	ModelPath   string // for local: path to GGUF model file
	HTTPTimeout time.Duration
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	// OpenAI lets us force tool use; for the agent loop we just let the
	// model choose and parse tool_calls off the response.
}

type Choice struct {
	Index   int     `json:"index"`
	Message Message `json:"message"`
}

type ChatResponse struct {
	Choices []Choice `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// ImageInput is one image attached to a vision request.
type ImageInput struct {
	Data     []byte
	MimeType string // e.g. "image/jpeg"; defaults to image/jpeg when empty
}

// visionContentPart is one part of an OpenAI-style multipart user message.
type visionContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *visionImageURL `json:"image_url,omitempty"`
}

type visionImageURL struct {
	URL string `json:"url"`
}

// visionMessage allows content to be either a plain string or multipart
// (text + image parts), which the OpenAI vision format requires.
type visionMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ChatVision sends a single-turn vision request to a remote provider using
// the OpenAI multipart content format (text + base64 data-URL image parts).
// The local source is NOT handled here — callers should route local vision
// through localai.Manager.ChatVision instead.
func (c *Client) ChatVision(ctx context.Context, model, system, prompt string, images []ImageInput) (*ChatResponse, error) {
	if c.opts.Source == SourceLocal {
		return nil, fmt.Errorf("local vision must be called via localai.Manager.ChatVision")
	}
	if model == "" {
		model = c.opts.Model
	}

	parts := []visionContentPart{{Type: "text", Text: prompt}}
	for _, img := range images {
		mime := img.MimeType
		if mime == "" {
			mime = "image/jpeg"
		}
		url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		parts = append(parts, visionContentPart{Type: "image_url", ImageURL: &visionImageURL{URL: url}})
	}

	payload := struct {
		Model    string          `json:"model"`
		Messages []visionMessage `json:"messages"`
	}{Model: model}

	if system != "" {
		sysContent, _ := json.Marshal(system)
		payload.Messages = append(payload.Messages, visionMessage{Role: "system", Content: sysContent})
	}
	userContent, err := json.Marshal(parts)
	if err != nil {
		return nil, err
	}
	payload.Messages = append(payload.Messages, visionMessage{Role: "user", Content: userContent})

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL()+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.opts.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	}
	if c.opts.Source == SourceOpenRouter {
		if c.opts.AppURL != "" {
			httpReq.Header.Set("HTTP-Referer", c.opts.AppURL)
		}
		if c.opts.AppName != "" {
			httpReq.Header.Set("X-Title", c.opts.AppName)
		}
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return &ChatResponse{Error: &struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		}{Message: string(raw)}}, nil
	}
	var out ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

type Client struct {
	opts Options
	http *http.Client
}

func New(opts Options) *Client {
	if opts.HTTPTimeout == 0 {
		opts.HTTPTimeout = 60 * time.Second
	}
	return &Client{opts: opts, http: &http.Client{Timeout: opts.HTTPTimeout}}
}

func (c *Client) baseURL() string {
	if c.opts.BaseURL != "" {
		return strings.TrimRight(c.opts.BaseURL, "/")
	}
	switch c.opts.Source {
	case SourceOpenAI:
		return "https://api.openai.com/v1"
	case SourceOpenRouter:
		return "https://openrouter.ai/api/v1"
	case SourceOllama:
		return "http://localhost:11434/v1"
	case SourceLMStudio:
		return "http://localhost:1234/v1"
	}
	return ""
}

// StreamChunk is one delta from a streaming completion.
type StreamChunk struct {
	Delta  string // incremental text (may be empty)
	Done   bool   // true on the final chunk
	ToolCalls []ToolCall // populated on the final chunk if the model used tools
	Error  string // non-empty if the stream failed
}

// ChatStream streams a completion from an OpenAI-compatible backend. It calls
// onDelta for every text delta and returns when the stream ends. The local
// source dispatches to LocalChatStreamFunc (token-by-token from llama.cpp).
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onDelta func(StreamChunk)) error {
	if c.opts.Source == SourceLocal {
		if err := initLocal(c.opts.ModelPath); err != nil {
			onDelta(StreamChunk{Error: err.Error()})
			return err
		}
		return localChatStream(ctx, req, onDelta)
	}

	if req.Model == "" {
		req.Model = c.opts.Model
	}
	// Mirror the request but ask the API to stream.
	body := struct {
		ChatRequest
		Stream bool `json:"stream"`
	}{ChatRequest: req, Stream: true}
	payload, err := json.Marshal(body)
	if err != nil {
		onDelta(StreamChunk{Error: err.Error()})
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL()+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		onDelta(StreamChunk{Error: err.Error()})
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.opts.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	}
	if c.opts.Source == SourceOpenRouter {
		if c.opts.AppURL != "" {
			httpReq.Header.Set("HTTP-Referer", c.opts.AppURL)
		}
		if c.opts.AppName != "" {
			httpReq.Header.Set("X-Title", c.opts.AppName)
		}
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		onDelta(StreamChunk{Error: fmt.Errorf("http: %w", err).Error()})
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		onDelta(StreamChunk{Error: string(raw)})
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(raw))
	}

	// Parse the SSE stream: lines "data: {json}" until "[DONE]".
	reader := bufio.NewReader(resp.Body)
	var toolAcc []ToolCall
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			onDelta(StreamChunk{Error: err.Error()})
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var sc struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &sc); err != nil {
			continue
		}
		if len(sc.Choices) == 0 {
			continue
		}
		for _, tc := range sc.Choices[0].Delta.ToolCalls {
			// Accumulate streamed tool calls by index.
			for len(toolAcc) <= tc.Index {
				toolAcc = append(toolAcc, ToolCall{})
			}
			if tc.ID != "" {
				toolAcc[tc.Index].ID = tc.ID
			}
			if tc.Type != "" {
				toolAcc[tc.Index].Type = tc.Type
			}
			if tc.Function.Name != "" {
				toolAcc[tc.Index].Function.Name = tc.Function.Name
			}
			toolAcc[tc.Index].Function.Arguments += tc.Function.Arguments
		}
		if sc.Choices[0].Delta.Content != "" {
			onDelta(StreamChunk{Delta: sc.Choices[0].Delta.Content})
		}
	}
	onDelta(StreamChunk{ToolCalls: toolAcc, Done: true})
	return nil
}

// ParseToolCallsFromText extracts [[TOOL_CALL]]-wrapped JSON tool calls from a
// local-model completion and returns them plus the cleaned (non-tool) text.
// Local models don't emit structured tool_calls, so they embed them in the
// generated text using this marker convention.
var toolCallRe = regexp.MustCompile(`(?s)\[\[TOOL_CALL\]\](\{.*?\})\[\[TOOL_CALL\]\]`)

func ParseToolCallsFromText(text string) ([]ToolCall, string) {
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

func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if c.opts.Source == SourceLocal {
		if err := initLocal(c.opts.ModelPath); err != nil {
			return nil, err
		}
		return localChat(ctx, req)
	}

	if req.Model == "" {
		req.Model = c.opts.Model
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL()+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.opts.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	}
	if c.opts.Source == SourceOpenRouter {
		if c.opts.AppURL != "" {
			httpReq.Header.Set("HTTP-Referer", c.opts.AppURL)
		}
		if c.opts.AppName != "" {
			httpReq.Header.Set("X-Title", c.opts.AppName)
		}
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return &ChatResponse{Error: &struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		}{Message: string(raw)}}, nil
	}
	var out ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}
