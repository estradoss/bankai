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
	if filepath.Ext(in.FilePath) == ".ipynb" {
		return readNotebook(in.FilePath)
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

// readNotebook renders a Jupyter .ipynb file as human-readable cells, including
// cell index, type, source, and any text outputs — matching how real Claude
// Code / vibelearn present notebooks to the model.
func readNotebook(path string) (Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	var nb struct {
		Cells []struct {
			CellType string          `json:"cell_type"`
			Source   json.RawMessage `json:"source"`
			Outputs  []struct {
				OutputType string                     `json:"output_type"`
				Text       json.RawMessage            `json:"text"`
				Name       string                     `json:"name"`
				Data       map[string]json.RawMessage `json:"data"`
				Ename      string                     `json:"ename"`
				Evalue     string                     `json:"evalue"`
			} `json:"outputs"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(raw, &nb); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("invalid notebook JSON: %v", err)}, nil
	}
	var b strings.Builder
	for i, c := range nb.Cells {
		fmt.Fprintf(&b, "<cell %d type=%q>\n%s\n", i, c.CellType, joinSource(c.Source))
		for _, o := range c.Outputs {
			switch o.OutputType {
			case "stream":
				fmt.Fprintf(&b, "[output %s]\n%s\n", o.Name, joinSource(o.Text))
			case "error":
				fmt.Fprintf(&b, "[error] %s: %s\n", o.Ename, o.Evalue)
			case "execute_result", "display_data":
				if t, ok := o.Data["text/plain"]; ok {
					fmt.Fprintf(&b, "[result]\n%s\n", joinSource(t))
				}
			}
		}
		b.WriteString("</cell>\n")
	}
	if b.Len() == 0 {
		return Result{Output: "(notebook has no cells)"}, nil
	}
	return Result{Output: b.String()}, nil
}

// joinSource handles the nbformat convention where source/text may be either a
// JSON string or an array of strings.
func joinSource(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return strings.Join(arr, "")
	}
	return string(raw)
}
