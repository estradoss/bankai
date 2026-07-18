// Package theme provides named color palettes for the TUI and line REPL. Colors
// are stored as ANSI 256-color index strings (lipgloss.Color-compatible) so the
// package stays decoupled from any rendering library. Ported from vibelearn's
// theme/output-style selection.
package theme

import (
	"sort"
	"strings"
)

// Palette is a set of semantic colors. Fields are ANSI-256 index strings.
type Palette struct {
	Name    string
	Accent  string // prompts, user marker
	Footer  string // status footer
	Error   string // error text
	Tool    string // tool-call panels
	Success string // success/diagnostic-clean
	Border  string // modal borders
}

var themes = map[string]Palette{
	"default":   {Name: "default", Accent: "6", Footer: "240", Error: "9", Tool: "5", Success: "2", Border: "11"},
	"dark":      {Name: "dark", Accent: "14", Footer: "244", Error: "9", Tool: "13", Success: "10", Border: "12"},
	"light":     {Name: "light", Accent: "4", Footer: "245", Error: "1", Tool: "5", Success: "2", Border: "4"},
	"dracula":   {Name: "dracula", Accent: "141", Footer: "61", Error: "203", Tool: "212", Success: "84", Border: "141"},
	"solarized": {Name: "solarized", Accent: "37", Footer: "242", Error: "160", Tool: "125", Success: "64", Border: "33"},
	"mono":      {Name: "mono", Accent: "15", Footer: "8", Error: "7", Tool: "7", Success: "15", Border: "7"},
}

// Default is the fallback palette.
var Default = themes["default"]

// Get returns the named palette (case-insensitive), falling back to Default and
// reporting whether the name was recognized.
func Get(name string) (Palette, bool) {
	p, ok := themes[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Default, false
	}
	return p, true
}

// Names returns the available theme names, sorted.
func Names() []string {
	out := make([]string, 0, len(themes))
	for n := range themes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
