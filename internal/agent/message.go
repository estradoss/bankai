package agent

import "encoding/json"

// ContentBlock is the tagged-union payload used inside a Message.
// Type is one of: "text", "tool_use", "tool_result", "thinking".
type ContentBlock struct {
	Type string `json:"type"`

	// text | thinking
	Text string `json:"text,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// Message is one turn in the conversation as sent to/from the model.
type Message struct {
	Role    string         `json:"role"` // "user" | "assistant" | "system"
	Content []ContentBlock `json:"content"`
}

// Usage tracks token accounting per turn.
type Usage struct {
	InputTokens             int `json:"input_tokens"`
	OutputTokens            int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens    int `json:"cache_read_input_tokens,omitempty"`
}

func (u Usage) Total() int { return u.InputTokens + u.OutputTokens }

// TextBlock returns a text-only user or assistant content block.
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// UserText is a shortcut for a single-text user Message.
func UserText(text string) Message {
	return Message{Role: "user", Content: []ContentBlock{TextBlock(text)}}
}
