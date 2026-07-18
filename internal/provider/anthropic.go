package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/estradoss/bankai/internal/agent"
	"github.com/estradoss/bankai/internal/tools"
)


const defaultBaseURL = "https://api.anthropic.com/v1"
const apiVersion = "2023-06-01"

// AuthSource yields the credential material used to sign a request.
// Implementations should refresh internally when needed.
type AuthSource interface {
	// Apply attaches auth headers to req and returns the anthropic-beta value
	// (if any) that should also be set. Called before every HTTP call.
	Apply(req *http.Request) error
}

// APIKeyAuth signs requests with x-api-key.
type APIKeyAuth struct{ Key string }

func (a APIKeyAuth) Apply(req *http.Request) error {
	req.Header.Set("x-api-key", a.Key)
	return nil
}

// BearerAuth signs requests with Authorization: Bearer + the oauth beta header.
// Token is resolved per call via TokenFunc so tokens can be refreshed lazily.
type BearerAuth struct {
	TokenFunc  func() (string, error)
	BetaHeader string
}

func (a BearerAuth) Apply(req *http.Request) error {
	tok, err := a.TokenFunc()
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "Bearer "+tok)
	if a.BetaHeader != "" {
		req.Header.Set("anthropic-beta", a.BetaHeader)
	}
	return nil
}

// Client is a thin Anthropic Messages API client with streaming.
type Client struct {
	Auth      AuthSource
	Model     string
	BaseURL   string
	HTTP      *http.Client
	SessionID string // sent as X-Claude-Code-Session-Id when non-empty
}

func NewClient(auth AuthSource, model string) *Client {
	return &Client{
		Auth:    auth,
		Model:   model,
		BaseURL: defaultBaseURL,
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
	}
}

// StreamRequest is the payload for /messages with stream=true.
type StreamRequest struct {
	Model     string          `json:"model"`
	System    string          `json:"system,omitempty"`
	Messages  []agent.Message `json:"messages"`
	Tools     []tools.Spec    `json:"tools,omitempty"`
	MaxTokens int             `json:"max_tokens"`
	Stream    bool            `json:"stream"`
}

// Event is one deserialized SSE event from the stream.
type Event struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message,omitempty"`
	Index   int             `json:"index,omitempty"`
	Delta   json.RawMessage `json:"delta,omitempty"`
	Content json.RawMessage `json:"content_block,omitempty"`
	Usage   *agent.Usage    `json:"usage,omitempty"`
}

// StreamResult is the assembled result after the stream ends.
type StreamResult struct {
	Content    []agent.ContentBlock
	StopReason string
	Usage      agent.Usage
}

// Stream calls /messages with stream=true and assembles the response.
// It calls onText(delta) for each text_delta so the caller can render live.
func (c *Client) Stream(ctx context.Context, req StreamRequest, onText func(string)) (*StreamResult, error) {
	req.Stream = true
	if req.MaxTokens == 0 {
		req.MaxTokens = 8192
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if err := c.Auth.Apply(httpReq); err != nil {
		return nil, err
	}
	httpReq.Header.Set("anthropic-version", apiVersion)
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")
	// Claude Code identifiers — required for OAuth-authenticated requests
	// to be accepted by the Anthropic API. Match the shape used by claude-cli
	// so the server routes us to the same policy pool.
	httpReq.Header.Set("x-app", "cli")
	httpReq.Header.Set("user-agent", "claude-cli/2.1.153 (external, cli)")
	if c.SessionID != "" {
		httpReq.Header.Set("x-claude-code-session-id", c.SessionID)
	}

	if os.Getenv("BANKAI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "\n--- REQUEST ---\n%s %s\n", httpReq.Method, httpReq.URL)
		for k, vs := range httpReq.Header {
			for _, v := range vs {
				if k == "Authorization" || k == "X-Api-Key" {
					v = v[:20] + "...redacted"
				}
				fmt.Fprintf(os.Stderr, "%s: %s\n", k, v)
			}
		}
		fmt.Fprintf(os.Stderr, "\n%s\n--- END REQUEST ---\n", string(body))
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic http %d: %s", resp.StatusCode, string(b))
	}

	return parseSSE(resp.Body, onText)
}

// blockAccum accumulates a single content block across delta events.
type blockAccum struct {
	Type      string
	Text      strings.Builder
	InputJSON strings.Builder
	ID        string
	Name      string
}

func parseSSE(r io.Reader, onText func(string)) (*StreamResult, error) {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	blocks := map[int]*blockAccum{}
	result := &StreamResult{}
	var dataBuf strings.Builder

	flush := func() error {
		if dataBuf.Len() == 0 {
			return nil
		}
		payload := dataBuf.String()
		dataBuf.Reset()
		var ev Event
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "message_start":
			var m struct {
				Usage agent.Usage `json:"usage"`
			}
			_ = json.Unmarshal(ev.Message, &m)
			result.Usage.InputTokens += m.Usage.InputTokens
			result.Usage.CacheCreationInputTokens += m.Usage.CacheCreationInputTokens
			result.Usage.CacheReadInputTokens += m.Usage.CacheReadInputTokens
		case "content_block_start":
			var b struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Text  string          `json:"text"`
				Input json.RawMessage `json:"input"`
			}
			_ = json.Unmarshal(ev.Content, &b)
			blocks[ev.Index] = &blockAccum{Type: b.Type, ID: b.ID, Name: b.Name}
			if b.Text != "" {
				blocks[ev.Index].Text.WriteString(b.Text)
			}
		case "content_block_delta":
			var d struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			}
			_ = json.Unmarshal(ev.Delta, &d)
			blk := blocks[ev.Index]
			if blk == nil {
				return nil
			}
			switch d.Type {
			case "text_delta":
				blk.Text.WriteString(d.Text)
				if onText != nil {
					onText(d.Text)
				}
			case "input_json_delta":
				blk.InputJSON.WriteString(d.PartialJSON)
			}
		case "content_block_stop":
			// nothing extra; assembled at end
		case "message_delta":
			var d struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage agent.Usage `json:"usage"`
			}
			_ = json.Unmarshal([]byte(payload), &d)
			if d.Delta.StopReason != "" {
				result.StopReason = d.Delta.StopReason
			}
			result.Usage.OutputTokens += d.Usage.OutputTokens
		case "message_stop":
			// finalize
		case "error":
			var e struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal([]byte(payload), &e)
			return fmt.Errorf("anthropic stream error: %s: %s", e.Error.Type, e.Error.Message)
		}
		return nil
	}

	for scan.Scan() {
		line := scan.Text()
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}

	// assemble blocks in index order
	max := -1
	for i := range blocks {
		if i > max {
			max = i
		}
	}
	for i := 0; i <= max; i++ {
		blk := blocks[i]
		if blk == nil {
			continue
		}
		cb := agent.ContentBlock{Type: blk.Type}
		switch blk.Type {
		case "text", "thinking":
			cb.Text = blk.Text.String()
		case "tool_use":
			cb.ID = blk.ID
			cb.Name = blk.Name
			raw := blk.InputJSON.String()
			if raw == "" {
				raw = "{}"
			}
			cb.Input = json.RawMessage(raw)
		}
		result.Content = append(result.Content, cb)
	}
	return result, nil
}
