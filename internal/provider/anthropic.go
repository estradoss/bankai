package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	fanthropic "charm.land/fantasy/providers/anthropic"

	"github.com/estradoss/bankai/internal/agent"
	"github.com/estradoss/bankai/internal/tools"
)

const defaultBaseURL = "https://api.anthropic.com/v1"

// AuthSource is the credential material used to authenticate a request. It is a
// closed set: either an API key (x-api-key) or a bearer OAuth token.
type AuthSource interface{ isAuth() }

// APIKeyAuth authenticates with an Anthropic API key (x-api-key).
type APIKeyAuth struct{ Key string }

func (APIKeyAuth) isAuth() {}

// BearerAuth authenticates with an OAuth bearer token + the oauth beta header.
// Token is resolved per call via TokenFunc so tokens can be refreshed lazily.
type BearerAuth struct {
	TokenFunc  func() (string, error)
	BetaHeader string
}

func (BearerAuth) isAuth() {}

// Client is a thin Anthropic Messages API client backed by charm.land/fantasy.
type Client struct {
	Auth      AuthSource
	Model     string
	BaseURL   string
	SessionID string // sent as X-Claude-Code-Session-Id when non-empty
	// Limits holds the most recent rate-limit headers seen on a response.
	Limits RateLimit

	mu       sync.Mutex
	provider fantasy.Provider
	model    fantasy.LanguageModel
	modelID  string
}

func NewClient(auth AuthSource, model string) *Client {
	base := os.Getenv("ANTHROPIC_BASE_URL")
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	return &Client{Auth: auth, Model: model, BaseURL: base}
}

// StreamRequest is the payload for one model turn.
type StreamRequest struct {
	Model     string
	System    string
	Messages  []agent.Message
	Tools     []tools.Spec
	MaxTokens int
}

// StreamResult is the assembled result after the stream ends.
type StreamResult struct {
	Content    []agent.ContentBlock
	StopReason string
	Usage      agent.Usage
}

// ensureModel lazily builds (and caches) the fantasy provider + language model.
// Returns the model and, for bearer auth, a func yielding fresh per-call headers.
func (c *Client) ensureModel(ctx context.Context, modelID string) (fantasy.LanguageModel, func() (map[string]string, error), error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var headerFn func() (map[string]string, error)
	switch a := c.Auth.(type) {
	case BearerAuth:
		headerFn = func() (map[string]string, error) {
			tok, err := a.TokenFunc()
			if err != nil {
				return nil, err
			}
			h := map[string]string{"Authorization": "Bearer " + tok}
			if a.BetaHeader != "" {
				h["anthropic-beta"] = a.BetaHeader
			}
			return h, nil
		}
	}

	if c.model != nil && c.modelID == modelID {
		return c.model, headerFn, nil
	}

	if c.provider == nil {
		opts := []fanthropic.Option{}
		if base := os.Getenv("ANTHROPIC_BASE_URL"); base != "" {
			opts = append(opts, fanthropic.WithBaseURL(base))
		}
		switch a := c.Auth.(type) {
		case APIKeyAuth:
			opts = append(opts, fanthropic.WithAPIKey(a.Key))
		case BearerAuth:
			// Skip x-api-key; auth flows through per-call Authorization header.
			opts = append(opts, fanthropic.WithSkipAuth(true))
		default:
			return nil, nil, fmt.Errorf("provider: unsupported auth source %T", c.Auth)
		}
		opts = append(opts, fanthropic.WithHTTPClient(&http.Client{
			Timeout:   10 * time.Minute,
			Transport: &captureRT{limits: &c.Limits},
		}))
		prov, err := fanthropic.New(opts...)
		if err != nil {
			return nil, nil, err
		}
		c.provider = prov
	}

	m, err := c.provider.LanguageModel(ctx, modelID)
	if err != nil {
		return nil, nil, err
	}
	c.model = m
	c.modelID = modelID
	return m, headerFn, nil
}

// captureRT is an http.RoundTripper wrapper (fanthropic.WithHTTPClient wants a
// Do-style client; *http.Client satisfies option.HTTPClient, so we hang this
// transport off it).
type captureRT struct {
	limits *RateLimit
}

func (t *captureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err == nil && resp != nil {
		t.limits.capture(resp.Header)
	}
	return resp, err
}

// Stream runs one model turn and assembles the response, calling onText(delta)
// for each text delta so the caller can render live.
func (c *Client) Stream(ctx context.Context, req StreamRequest, onText func(string)) (*StreamResult, error) {
	modelID := req.Model
	if modelID == "" {
		modelID = c.Model
	}
	m, headerFn, err := c.ensureModel(ctx, modelID)
	if err != nil {
		return nil, err
	}

	maxTok := int64(req.MaxTokens)
	if maxTok == 0 {
		maxTok = 8192
	}
	call := fantasy.Call{
		Prompt:          buildPrompt(req.System, req.Messages),
		Tools:           buildTools(req.Tools),
		MaxOutputTokens: &maxTok,
	}
	if headerFn != nil {
		h, err := headerFn()
		if err != nil {
			return nil, err
		}
		call.Headers = h
	}

	stream, err := m.Stream(ctx, call)
	if err != nil {
		return nil, err
	}

	result := &StreamResult{}
	for part := range stream {
		switch part.Type {
		case fantasy.StreamPartTypeTextDelta:
			appendText(&result.Content, "text", part.Delta)
			if onText != nil {
				onText(part.Delta)
			}
		case fantasy.StreamPartTypeReasoningDelta:
			appendText(&result.Content, "thinking", part.Delta)
		case fantasy.StreamPartTypeToolCall:
			raw := part.ToolCallInput
			if raw == "" {
				raw = "{}"
			}
			result.Content = append(result.Content, agent.ContentBlock{
				Type:  "tool_use",
				ID:    part.ID,
				Name:  part.ToolCallName,
				Input: json.RawMessage(raw),
			})
		case fantasy.StreamPartTypeFinish:
			result.StopReason = mapStop(part.FinishReason)
			result.Usage = mapUsage(part.Usage)
		case fantasy.StreamPartTypeError:
			if part.Error != nil {
				return nil, part.Error
			}
			return nil, errors.New("anthropic stream error")
		}
	}
	return result, nil
}

// appendText appends delta to the last content block if it is of typ, else
// starts a new one — keeps streamed text/thinking coalesced and in order.
func appendText(content *[]agent.ContentBlock, typ, delta string) {
	if delta == "" {
		return
	}
	n := len(*content)
	if n > 0 && (*content)[n-1].Type == typ {
		(*content)[n-1].Text += delta
		return
	}
	*content = append(*content, agent.ContentBlock{Type: typ, Text: delta})
}

func mapStop(fr fantasy.FinishReason) string {
	if fr == fantasy.FinishReasonToolCalls {
		return "tool_use"
	}
	return "end_turn"
}

func mapUsage(u fantasy.Usage) agent.Usage {
	return agent.Usage{
		InputTokens:              int(u.InputTokens),
		OutputTokens:             int(u.OutputTokens),
		CacheCreationInputTokens: int(u.CacheCreationTokens),
		CacheReadInputTokens:     int(u.CacheReadTokens),
	}
}

// buildPrompt converts bankai's system string + message history into a fantasy
// Prompt. bankai keeps tool results inside a user message; fantasy wants them in
// a dedicated tool-role message.
func buildPrompt(system string, msgs []agent.Message) fantasy.Prompt {
	prompt := fantasy.Prompt{}
	if strings.TrimSpace(system) != "" {
		prompt = append(prompt, fantasy.NewSystemMessage(system))
	}
	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			var parts []fantasy.MessagePart
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					parts = append(parts, fantasy.TextPart{Text: b.Text})
				case "thinking":
					parts = append(parts, fantasy.ReasoningPart{Text: b.Text})
				case "tool_use":
					parts = append(parts, fantasy.ToolCallPart{
						ToolCallID: b.ID,
						ToolName:   b.Name,
						Input:      string(b.Input),
					})
				}
			}
			if len(parts) > 0 {
				prompt = append(prompt, fantasy.Message{Role: fantasy.MessageRoleAssistant, Content: parts})
			}
		default: // user
			var userParts, toolParts []fantasy.MessagePart
			for _, b := range m.Content {
				switch b.Type {
				case "tool_result":
					var out fantasy.ToolResultOutputContent
					if b.IsError {
						out = fantasy.ToolResultOutputContentError{Error: errors.New(b.Content)}
					} else {
						out = fantasy.ToolResultOutputContentText{Text: b.Content}
					}
					toolParts = append(toolParts, fantasy.ToolResultPart{
						ToolCallID: b.ToolUseID,
						Output:     out,
					})
				default: // text
					userParts = append(userParts, fantasy.TextPart{Text: b.Text})
				}
			}
			if len(userParts) > 0 {
				prompt = append(prompt, fantasy.Message{Role: fantasy.MessageRoleUser, Content: userParts})
			}
			if len(toolParts) > 0 {
				prompt = append(prompt, fantasy.Message{Role: fantasy.MessageRoleTool, Content: toolParts})
			}
		}
	}
	return prompt
}

// buildTools converts bankai tool specs into fantasy function tools.
func buildTools(specs []tools.Spec) []fantasy.Tool {
	if len(specs) == 0 {
		return nil
	}
	out := make([]fantasy.Tool, 0, len(specs))
	for _, s := range specs {
		schema := map[string]any{}
		if len(s.InputSchema) > 0 {
			_ = json.Unmarshal(s.InputSchema, &schema)
		}
		// fantasy's anthropic provider asserts required to []string; JSON
		// unmarshals arrays as []any, so coerce it.
		if req, ok := schema["required"].([]any); ok {
			ss := make([]string, 0, len(req))
			for _, r := range req {
				if s, ok := r.(string); ok {
					ss = append(ss, s)
				}
			}
			schema["required"] = ss
		}
		out = append(out, fantasy.FunctionTool{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: schema,
		})
	}
	return out
}
