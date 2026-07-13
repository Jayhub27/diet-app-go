package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
