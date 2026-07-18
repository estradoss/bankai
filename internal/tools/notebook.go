package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// NotebookEditTool edits cells of a Jupyter notebook (.ipynb). Ported from
// vibelearn's NotebookEditTool: replace/insert/delete a cell identified by id
// or 0-indexed position.
type NotebookEditTool struct{}

func (NotebookEditTool) Name() string { return "NotebookEdit" }

func (NotebookEditTool) Description() string {
	return "Completely replaces the contents of a specific cell in a Jupyter notebook (.ipynb) with new source. " +
		"notebook_path must be absolute. cell_id may be a cell's id or its 0-indexed position. " +
		"edit_mode=insert adds a new cell after cell_id (or at the start if omitted); edit_mode=delete removes it. " +
		"cell_type is required when edit_mode=insert."
}

func (NotebookEditTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"notebook_path": {"type": "string", "description": "Absolute path to the .ipynb file"},
			"cell_id": {"type": "string", "description": "Cell id or 0-indexed position of the cell to edit"},
			"new_source": {"type": "string", "description": "New source for the cell"},
			"cell_type": {"type": "string", "enum": ["code", "markdown"], "description": "Cell type; required for insert"},
			"edit_mode": {"type": "string", "enum": ["replace", "insert", "delete"], "description": "Defaults to replace"}
		},
		"required": ["notebook_path", "new_source"]
	}`)
}

func (NotebookEditTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		NotebookPath string `json:"notebook_path"`
		CellID       string `json:"cell_id"`
		NewSource    string `json:"new_source"`
		CellType     string `json:"cell_type"`
		EditMode     string `json:"edit_mode"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if !filepath.IsAbs(in.NotebookPath) {
		return Result{IsError: true, Output: "notebook_path must be absolute"}, nil
	}
	mode := in.EditMode
	if mode == "" {
		mode = "replace"
	}
	if mode != "replace" && mode != "insert" && mode != "delete" {
		return Result{IsError: true, Output: "edit_mode must be replace, insert, or delete"}, nil
	}
	if mode == "insert" && in.CellType == "" {
		return Result{IsError: true, Output: "cell_type is required when edit_mode=insert"}, nil
	}
	if filepath.Ext(in.NotebookPath) != ".ipynb" {
		return Result{IsError: true, Output: "file must be a .ipynb notebook"}, nil
	}

	raw, err := os.ReadFile(in.NotebookPath)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	// Preserve unknown top-level keys by decoding into a generic map.
	var nb map[string]any
	if err := json.Unmarshal(raw, &nb); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("invalid notebook JSON: %v", err)}, nil
	}
	cellsAny, _ := nb["cells"].([]any)
	cells := make([]map[string]any, 0, len(cellsAny))
	for _, c := range cellsAny {
		if m, ok := c.(map[string]any); ok {
			cells = append(cells, m)
		}
	}

	// Resolve target index from cell_id (id match, else numeric position).
	findIndex := func() int {
		if in.CellID == "" {
			return -1
		}
		for i, c := range cells {
			if id, ok := c["id"].(string); ok && id == in.CellID {
				return i
			}
		}
		if n, err := strconv.Atoi(in.CellID); err == nil && n >= 0 && n < len(cells) {
			return n
		}
		return -2 // not found
	}

	sourceLines := splitKeepNL(in.NewSource)

	switch mode {
	case "replace":
		idx := findIndex()
		if idx < 0 {
			return Result{IsError: true, Output: "cell_id must identify an existing cell for replace"}, nil
		}
		cells[idx]["source"] = sourceLines
		if in.CellType != "" {
			cells[idx]["cell_type"] = in.CellType
		}
	case "insert":
		insertAt := 0
		if in.CellID != "" {
			idx := findIndex()
			if idx == -2 {
				return Result{IsError: true, Output: "cell_id not found"}, nil
			}
			if idx >= 0 {
				insertAt = idx + 1
			}
		}
		newCell := map[string]any{
			"cell_type": in.CellType,
			"metadata":  map[string]any{},
			"source":    sourceLines,
		}
		if in.CellType == "code" {
			newCell["outputs"] = []any{}
			newCell["execution_count"] = nil
		}
		cells = append(cells, nil)
		copy(cells[insertAt+1:], cells[insertAt:])
		cells[insertAt] = newCell
	case "delete":
		idx := findIndex()
		if idx < 0 {
			return Result{IsError: true, Output: "cell_id must identify an existing cell for delete"}, nil
		}
		cells = append(cells[:idx], cells[idx+1:]...)
	}

	// Write cells back.
	nbCells := make([]any, len(cells))
	for i := range cells {
		nbCells[i] = cells[i]
	}
	nb["cells"] = nbCells
	out, err := json.MarshalIndent(nb, "", " ")
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	out = append(out, '\n')
	if err := os.WriteFile(in.NotebookPath, out, 0o644); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("%s %s: %d cells", in.NotebookPath, mode, len(cells))}, nil
}

// splitKeepNL splits source into a list of lines keeping trailing newlines, the
// nbformat convention for cell source arrays.
func splitKeepNL(s string) []any {
	if s == "" {
		return []any{}
	}
	var out []any
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
