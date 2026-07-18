package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/estradoss/bankai/internal/lsp"
)

// LSPTool asks a language server for diagnostics on a file. Port of vibelearn's
// LSPTool. It starts the appropriate server on first use (by file extension).
type LSPTool struct{ Mgr *lsp.Manager }

func (LSPTool) Name() string { return "lsp_diagnostics" }
func (LSPTool) Description() string {
	return "Get language-server diagnostics (errors, warnings) for a source file. Use after editing to check your change compiles/type-checks. Returns an empty result if no language server is configured for the file type."
}
func (LSPTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {"file": {"type": "string", "description": "Path to the source file to check"}},
		"required": ["file"]
	}`)
}
func (t LSPTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Mgr == nil {
		return Result{IsError: true, Output: "no language servers configured"}, nil
	}
	var in struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.File == "" {
		return Result{IsError: true, Output: "file is required"}, nil
	}
	diags, err := t.Mgr.Diagnose(ctx, in.File)
	if err != nil {
		return Result{IsError: true, Output: "lsp error: " + err.Error()}, nil
	}
	if diags == nil {
		return Result{Output: "no language server for this file type"}, nil
	}
	if len(diags) == 0 {
		return Result{Output: "no diagnostics (clean)"}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d diagnostic(s) in %s:\n", len(diags), in.File)
	for _, d := range diags {
		src := d.Source
		if src != "" {
			src = " [" + src + "]"
		}
		fmt.Fprintf(&b, "  %d:%d %s%s: %s\n",
			d.Range.Start.Line+1, d.Range.Start.Character+1, lsp.SeverityName(d.Severity), src, d.Message)
	}
	return Result{Output: strings.TrimRight(b.String(), "\n")}, nil
}

// LSPHoverTool asks a language server for hover info at a position (ported from
// vibelearn's LSP hover). Line/character are 1-based in the tool interface.
type LSPHoverTool struct{ Mgr *lsp.Manager }

func (LSPHoverTool) Name() string { return "lsp_hover" }
func (LSPHoverTool) Description() string {
	return "Get language-server hover info (type/signature/doc) at a position in a source file. line and character are 1-based. Returns empty if no server is configured or there is no hover."
}
func (LSPHoverTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file": {"type": "string", "description": "Path to the source file"},
			"line": {"type": "integer", "description": "1-based line number"},
			"character": {"type": "integer", "description": "1-based column"}
		},
		"required": ["file", "line", "character"]
	}`)
}
func (t LSPHoverTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Mgr == nil {
		return Result{IsError: true, Output: "no language servers configured"}, nil
	}
	var in struct {
		File      string `json:"file"`
		Line      int    `json:"line"`
		Character int    `json:"character"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.File == "" {
		return Result{IsError: true, Output: "file is required"}, nil
	}
	h, err := t.Mgr.Hover(ctx, in.File, in.Line-1, in.Character-1)
	if err != nil {
		return Result{IsError: true, Output: "lsp error: " + err.Error()}, nil
	}
	if strings.TrimSpace(h) == "" {
		return Result{Output: "no hover info at this position"}, nil
	}
	return Result{Output: h}, nil
}

// LSPDefinitionTool asks a language server for the definition location(s) at a
// position (ported from vibelearn's LSP go-to-definition). 1-based line/char.
type LSPDefinitionTool struct{ Mgr *lsp.Manager }

func (LSPDefinitionTool) Name() string { return "lsp_definition" }
func (LSPDefinitionTool) Description() string {
	return "Get the definition location(s) for the symbol at a position in a source file. line and character are 1-based. Returns file:line:col locations, or empty if none."
}
func (LSPDefinitionTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file": {"type": "string", "description": "Path to the source file"},
			"line": {"type": "integer", "description": "1-based line number"},
			"character": {"type": "integer", "description": "1-based column"}
		},
		"required": ["file", "line", "character"]
	}`)
}
func (t LSPDefinitionTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Mgr == nil {
		return Result{IsError: true, Output: "no language servers configured"}, nil
	}
	var in struct {
		File      string `json:"file"`
		Line      int    `json:"line"`
		Character int    `json:"character"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.File == "" {
		return Result{IsError: true, Output: "file is required"}, nil
	}
	locs, err := t.Mgr.Definition(ctx, in.File, in.Line-1, in.Character-1)
	if err != nil {
		return Result{IsError: true, Output: "lsp error: " + err.Error()}, nil
	}
	if len(locs) == 0 {
		return Result{Output: "no definition found at this position"}, nil
	}
	var b strings.Builder
	for _, l := range locs {
		path := strings.TrimPrefix(l.URI, "file://")
		fmt.Fprintf(&b, "%s:%d:%d\n", path, l.Range.Start.Line+1, l.Range.Start.Character+1)
	}
	return Result{Output: strings.TrimRight(b.String(), "\n")}, nil
}

// LSPRenameTool renames a symbol project-wide via the language server and
// applies the edits to disk (ported from vibelearn's LSP rename). 1-based pos.
type LSPRenameTool struct{ Mgr *lsp.Manager }

func (LSPRenameTool) Name() string { return "lsp_rename" }
func (LSPRenameTool) Description() string {
	return "Rename the symbol at a position across the project using the language server, applying the edits to disk. line and character are 1-based. Returns the files changed."
}
func (LSPRenameTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file": {"type": "string", "description": "Path to the source file"},
			"line": {"type": "integer", "description": "1-based line number"},
			"character": {"type": "integer", "description": "1-based column"},
			"new_name": {"type": "string", "description": "New symbol name"}
		},
		"required": ["file", "line", "character", "new_name"]
	}`)
}
func (t LSPRenameTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Mgr == nil {
		return Result{IsError: true, Output: "no language servers configured"}, nil
	}
	var in struct {
		File      string `json:"file"`
		Line      int    `json:"line"`
		Character int    `json:"character"`
		NewName   string `json:"new_name"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.File == "" || in.NewName == "" {
		return Result{IsError: true, Output: "file and new_name are required"}, nil
	}
	n, files, err := t.Mgr.Rename(ctx, in.File, in.Line-1, in.Character-1, in.NewName)
	if err != nil {
		return Result{IsError: true, Output: "lsp error: " + err.Error()}, nil
	}
	if n == 0 {
		return Result{Output: "no rename edits produced (is the cursor on a renameable symbol?)"}, nil
	}
	return Result{Output: fmt.Sprintf("renamed to %q across %d file(s):\n%s", in.NewName, n, strings.Join(files, "\n"))}, nil
}
