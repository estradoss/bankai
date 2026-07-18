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

// CodexBaseURL is ChatGPT's Codex backend (OpenAI Responses API shape).
const CodexBaseURL = "https://chatgpt.com/backend-api/codex/responses"

// DefaultCodexModel is used when no Codex-specific model is chosen.
const DefaultCodexModel = "gpt-5.2-codex"

var codexModels = map[string]bool{
	"gpt-5.2-codex": true, "gpt-5.1-codex": true, "gpt-5.1-codex-mini": true,
	"gpt-5.1-codex-max": true, "gpt-5.4": true, "gpt-5.2": true,
}

// MapToCodexModel maps a configured model name to a Codex model id, mirroring
// the TypeScript adapter's mapping so Claude-style names still route sensibly.
func MapToCodexModel(model string) string {
	if model == "" {
		return DefaultCodexModel
	}
	if codexModels[model] {
		return model
	}
	l := strings.ToLower(model)
	switch {
	case strings.Contains(l, "opus"):
		return "gpt-5.1-codex-max"
	case strings.Contains(l, "haiku"):
		return "gpt-5.1-codex-mini"
	case strings.Contains(l, "sonnet"):
		return "gpt-5.2-codex"
	}
	return DefaultCodexModel
}

// OpenAIClient talks to ChatGPT's Codex backend using OpenAI subscription OAuth
// tokens, translating to/from the Anthropic-shaped types used throughout bankai.
// Selected via CLAUDE_CODE_USE_OPENAI=1 with stored Codex credentials.
type OpenAIClient struct {
	TokenFunc func() (string, error)
	AccountID string
	BaseURL   string
	HTTP      *http.Client
}

func NewCodexClient(tokenFunc func() (string, error), accountID string) *OpenAIClient {
	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		base = CodexBaseURL
	}
	return &OpenAIClient{
		TokenFunc: tokenFunc,
		AccountID: accountID,
		BaseURL:   base,
		HTTP:      &http.Client{Timeout: 10 * time.Minute},
	}
}

// ---- request translation (Anthropic shape -> Codex Responses input) ----

type codexReq struct {
	Model             string           `json:"model"`
	Store             bool             `json:"store"`
	Stream            bool             `json:"stream"`
	Instructions      string           `json:"instructions"`
	Input             []map[string]any `json:"input"`
	ToolChoice        string           `json:"tool_choice"`
	ParallelToolCalls bool             `json:"parallel_tool_calls"`
	Tools             []map[string]any `json:"tools,omitempty"`
}

func toCodexInput(msgs []agent.Message) []map[string]any {
	var input []map[string]any
	for _, m := range msgs {
		switch m.Role {
		case "user":
			var textParts []map[string]any
			for _, c := range m.Content {
				switch c.Type {
				case "text":
					textParts = append(textParts, map[string]any{"type": "input_text", "text": c.Text})
				case "tool_result":
					input = append(input, map[string]any{
						"type":    "function_call_output",
						"call_id": c.ToolUseID,
						"output":  c.Content,
					})
				}
			}
			if len(textParts) == 1 {
				input = append(input, map[string]any{"role": "user", "content": textParts[0]["text"]})
			} else if len(textParts) > 1 {
				input = append(input, map[string]any{"role": "user", "content": textParts})
			}
		case "assistant":
			for _, c := range m.Content {
				switch c.Type {
				case "text":
					input = append(input, map[string]any{
						"type":    "message",
						"role":    "assistant",
						"content": []map[string]any{{"type": "output_text", "text": c.Text, "annotations": []any{}}},
						"status":  "completed",
					})
				case "tool_use":
					args := string(c.Input)
					if args == "" {
						args = "{}"
					}
					input = append(input, map[string]any{
						"type":      "function_call",
						"call_id":   c.ID,
						"name":      c.Name,
						"arguments": args,
					})
				}
			}
		}
	}
	return input
}

func toCodexTools(specs []tools.Spec) []map[string]any {
	if len(specs) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		var params any
		if len(s.InputSchema) > 0 {
			params = json.RawMessage(s.InputSchema)
		} else {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"type":        "function",
			"name":        s.Name,
			"description": s.Description,
			"parameters":  params,
			"strict":      nil,
		})
	}
	return out
}

// ---- streaming ----

func (c *OpenAIClient) Stream(ctx context.Context, req StreamRequest, onText func(string)) (*StreamResult, error) {
	body, err := json.Marshal(codexReq{
		Model:             MapToCodexModel(req.Model),
		Store:             false,
		Stream:            true,
		Instructions:      req.System,
		Input:             toCodexInput(req.Messages),
		ToolChoice:        "auto",
		ParallelToolCalls: true,
		Tools:             toCodexTools(req.Tools),
	})
	if err != nil {
		return nil, err
	}
	token, err := c.TokenFunc()
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")
	httpReq.Header.Set("authorization", "Bearer "+token)
	httpReq.Header.Set("chatgpt-account-id", c.AccountID)
	httpReq.Header.Set("originator", "bankai")
	httpReq.Header.Set("openai-beta", "responses=experimental")

	if os.Getenv("BANKAI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "\n--- CODEX REQUEST ---\n%s\n--- END ---\n", string(body))
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("codex http %d: %s", resp.StatusCode, string(b))
	}
	return parseCodexSSE(resp.Body, onText)
}

// codexEvent is the subset of Responses SSE fields we consume.
type codexEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	Item  struct {
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	Arguments string `json:"arguments"`
	Response  struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"response"`
}

func parseCodexSSE(r io.Reader, onText func(string)) (*StreamResult, error) {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	result := &StreamResult{}
	var text, thinking strings.Builder
	// function call being assembled
	var curCallID, curName string
	var curArgs strings.Builder
	inToolCall := false
	hadTool := false

	flushTool := func() {
		if !inToolCall {
			return
		}
		args := curArgs.String()
		if args == "" {
			args = "{}"
		}
		result.Content = append(result.Content, agent.ContentBlock{
			Type: "tool_use", ID: curCallID, Name: curName, Input: json.RawMessage(args),
		})
		inToolCall = false
		curArgs.Reset()
		curCallID, curName = "", ""
	}

	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "response.output_item.added":
			if ev.Item.Type == "function_call" {
				flushTool()
				curCallID = ev.Item.CallID
				curName = ev.Item.Name
				curArgs.Reset()
				curArgs.WriteString(ev.Item.Arguments)
				inToolCall = true
				hadTool = true
			}
		case "response.output_text.delta":
			if ev.Delta != "" {
				text.WriteString(ev.Delta)
				if onText != nil {
					onText(ev.Delta)
				}
			}
		case "response.reasoning.delta":
			thinking.WriteString(ev.Delta)
		case "response.function_call_arguments.delta":
			if inToolCall {
				curArgs.WriteString(ev.Delta)
			}
		case "response.function_call_arguments.done":
			if inToolCall && ev.Arguments != "" {
				curArgs.Reset()
				curArgs.WriteString(ev.Arguments)
			}
		case "response.output_item.done":
			if ev.Item.Type == "function_call" {
				flushTool()
			}
		case "response.completed":
			result.Usage.InputTokens = ev.Response.Usage.InputTokens
			result.Usage.OutputTokens = ev.Response.Usage.OutputTokens
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	flushTool()

	// Assemble content: thinking, then text, then tool_use blocks already appended.
	var head []agent.ContentBlock
	if t := strings.TrimSpace(thinking.String()); t != "" {
		head = append(head, agent.ContentBlock{Type: "thinking", Text: thinking.String()})
	}
	if t := strings.TrimSpace(text.String()); t != "" {
		head = append(head, agent.ContentBlock{Type: "text", Text: text.String()})
	}
	result.Content = append(head, result.Content...)

	if hadTool {
		result.StopReason = "tool_use"
	} else {
		result.StopReason = "end_turn"
	}
	return result, nil
}
