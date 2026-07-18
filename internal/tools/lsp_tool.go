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
