package tui

import (
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
	input = strings.TrimSpace(input)
	// Pull a bash command out of {"command":"..."} so it reads as a command,
	// not JSON.
	if strings.HasPrefix(input, "{") && strings.Contains(input, "\"command\"") {
		if i := strings.Index(input, "\"command\""); i >= 0 {
			rest := input[i+len("\"command\""):]
			if j := strings.Index(rest, "\""); j >= 0 {
				rest = rest[j+1:]
				if k := strings.Index(rest, "\""); k >= 0 {
					return lipgloss.NewStyle().Bold(true).Render("⚙ "+name) + "  " + rest[:k]
				}
			}
		}
	}
	if len(input) > 120 {
		input = input[:117] + "…"
	}
	return lipgloss.NewStyle().Bold(true).Render("⚙ "+name) + "  " + input
}
