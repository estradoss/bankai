package tui

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// Markdown rendering for assistant output, using glamour (the same renderer
// Charm's own tools use). Renderers are memoized per width; the transcript is
// built from typed blocks (see Bubble.blocks) so assistant prose renders as
// real markdown — headings, lists, bold, fenced code with syntax highlight —
// while user/tool/notice lines keep their own compact styling.

var (
	mdMu    sync.Mutex
	mdCache = map[int]*glamour.TermRenderer{}
)

func mdRenderer(width int) *glamour.TermRenderer {
	if width < 20 {
		width = 20
	}
	mdMu.Lock()
	defer mdMu.Unlock()
	if r, ok := mdCache[width]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	mdCache[width] = r
	return r
}

// renderMarkdown renders s as terminal markdown at the given width. On any
// failure it falls back to the raw string so text is never lost.
func renderMarkdown(s string, width int) string {
	r := mdRenderer(width)
	if r == nil {
		return s
	}
	out, err := r.Render(s)
	if err != nil {
		return s
	}
	// glamour adds a leading/trailing blank line; trim to keep blocks tight.
	return strings.Trim(out, "\n")
}

// blockKind classifies a transcript entry.
type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockTool
	blockNotice
)

// block is one entry in the scrollback transcript.
type block struct {
	kind blockKind
	text string
}

// render turns a block into styled terminal text at the given width.
func (bl block) render(width int) string {
	switch bl.kind {
	case blockUser:
		label := userStyle.Render("❯ ")
		return label + userStyle.Render(bl.text)
	case blockAssistant:
		return renderMarkdown(bl.text, width)
	case blockTool:
		return toolBox.Render(bl.text)
	case blockNotice:
		return suggDim.Render(bl.text)
	}
	return bl.text
}

// renderBlocks assembles the whole transcript: banner first, then each block
// separated by a blank line.
func renderBlocks(banner string, blocks []block, width int) string {
	var parts []string
	if banner != "" {
		parts = append(parts, banner)
	}
	for _, bl := range blocks {
		s := strings.TrimRight(bl.render(width), "\n")
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// prettyToolLine formats a tool call for its panel: NAME  <compact args>.
func prettyToolLine(name, input string) string {
	arg := toolArgSummary(name, input)
	if len(arg) > 120 {
		arg = arg[:119] + "…"
	}
	head := lipgloss.NewStyle().Bold(true).Render("⚙ " + name)
	if arg == "" {
		return head
	}
	return head + "  " + arg
}

// toolArgSummary extracts a one-line, human-readable argument summary from a
// tool's raw JSON input — the command for Bash, the path for file tools, the
// pattern for search tools, etc. Falls back to compacted JSON.
func toolArgSummary(name, input string) string {
	input = strings.TrimSpace(input)
	var m map[string]any
	if json.Unmarshal([]byte(input), &m) != nil {
		return input
	}
	str := func(k string) string {
		if v, ok := m[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	// Preferred field per tool, in priority order.
	for _, k := range []string{"command", "file_path", "pattern", "path", "url", "query", "description", "prompt", "notebook_path"} {
		if v := str(k); v != "" {
			// Grep/Glob read nicer as `pattern  in path`.
			if (k == "pattern") && str("path") != "" {
				return v + "  in " + str("path")
			}
			return firstLine(v)
		}
	}
	// Unknown shape: compact the JSON onto one line.
	if b, err := json.Marshal(m); err == nil {
		return string(b)
	}
	return input
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
