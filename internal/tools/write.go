package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type WriteTool struct{}

func (WriteTool) Name() string { return "Write" }

func (WriteTool) Description() string {
	return "Write content to a file (create or overwrite). Absolute paths only. Creates parent directories."
}

func (WriteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file_path": {"type": "string"},
			"content":   {"type": "string"}
		},
		"required": ["file_path", "content"]
	}`)
}

func (WriteTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if !filepath.IsAbs(in.FilePath) {
		return Result{IsError: true, Output: "file_path must be absolute"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(in.FilePath), 0o755); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	if err := os.WriteFile(in.FilePath, []byte(in.Content), 0o644); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("wrote %s (%d bytes)", in.FilePath, len(in.Content))}, nil
}
