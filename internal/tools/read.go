package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ReadTool struct{}

func (ReadTool) Name() string { return "Read" }

func (ReadTool) Description() string {
	return "Read a file. Absolute paths only. Returns cat -n style numbered lines. Supports optional offset (start line, 1-based) and limit (line count, default 2000)."
}

func (ReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file_path": {"type": "string", "description": "Absolute path"},
			"offset": {"type": "integer", "description": "Start line (1-based)"},
			"limit":  {"type": "integer", "description": "Max lines (default 2000)"}
		},
		"required": ["file_path"]
	}`)
}

func (ReadTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if !filepath.IsAbs(in.FilePath) {
		return Result{IsError: true, Output: "file_path must be absolute"}, nil
	}
	f, err := os.Open(in.FilePath)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	defer f.Close()
	if in.Offset <= 0 {
		in.Offset = 1
	}
	if in.Limit <= 0 {
		in.Limit = 2000
	}
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var buf strings.Builder
	lineNo := 0
	shown := 0
	for scan.Scan() {
		lineNo++
		if lineNo < in.Offset {
			continue
		}
		if shown >= in.Limit {
			break
		}
		fmt.Fprintf(&buf, "%6d\t%s\n", lineNo, scan.Text())
		shown++
	}
	if err := scan.Err(); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	if buf.Len() == 0 {
		return Result{Output: "(empty file or offset past end)"}, nil
	}
	return Result{Output: buf.String()}, nil
}
