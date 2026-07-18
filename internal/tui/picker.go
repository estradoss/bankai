package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/estradoss/bankai/internal/transcript"
)

// picker is a modal single-select overlay used by /model and /resume. It shows
// a titled list; ↑/↓ move, enter selects, esc cancels. onSelect receives the
// chosen item's value.
type picker struct {
	title    string
	items    []pickerItem
	idx      int
	onSelect func(value string) tea.Cmd
}

type pickerItem struct {
	label string // shown
	desc  string // dim, right/second line
	value string // passed to onSelect
}

var (
	pickerBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("6")).
			Padding(1, 2)
	pickerTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	pickerSel   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
)

// handleKey processes a key for the open picker. Returns (cmd, done): done=true
// means the picker should close.
func (p *picker) handleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "up", "ctrl+p", "k":
		p.idx = (p.idx - 1 + len(p.items)) % len(p.items)
	case "down", "ctrl+n", "j":
		p.idx = (p.idx + 1) % len(p.items)
	case "enter":
		if p.idx >= 0 && p.idx < len(p.items) {
			cmd := p.onSelect(p.items[p.idx].value)
			return cmd, true
		}
		return nil, true
	case "esc", "ctrl+c":
		return nil, true
	}
	return nil, false
}

func (p *picker) view(width int) string {
	var rows []string
	rows = append(rows, pickerTitle.Render(p.title), "")
	for i, it := range p.items {
		line := it.label
		if it.desc != "" {
			line += "  " + suggDim.Render(it.desc)
		}
		if i == p.idx {
			line = pickerSel.Render("▸ ") + pickerSel.Render(it.label)
			if it.desc != "" {
				line += "  " + suggDim.Render(it.desc)
			}
		} else {
			line = "  " + line
		}
		rows = append(rows, line)
	}
	rows = append(rows, "", suggDim.Render("↑/↓ move · enter select · esc cancel"))
	bw := width - 4
	if bw > 76 {
		bw = 76
	}
	return pickerBox.Width(bw).Render(strings.Join(rows, "\n"))
}

// openModelPicker shows the model selector, seeded with common Claude models
// plus the currently-configured one.
func (b *Bubble) openModelPicker() {
	models := []pickerItem{
		{label: "claude-opus-4-8", desc: "most capable", value: "claude-opus-4-8"},
		{label: "claude-sonnet-5", desc: "balanced", value: "claude-sonnet-5"},
		{label: "claude-haiku-4-5-20251001", desc: "fastest", value: "claude-haiku-4-5-20251001"},
		{label: "claude-fable-5", desc: "creative", value: "claude-fable-5"},
	}
	cur := b.engine.Client.Model
	found := false
	for i, m := range models {
		if m.value == cur {
			b.picker = &picker{title: "Select model", items: models, idx: i}
			found = true
			break
		}
	}
	if !found {
		models = append([]pickerItem{{label: cur, desc: "current", value: cur}}, models...)
		b.picker = &picker{title: "Select model", items: models}
	} else {
		b.picker.title = "Select model"
	}
	b.picker.onSelect = func(v string) tea.Cmd {
		b.engine.Client.Model = v
		b.pushBlock(blockNotice, "model set to "+v)
		return nil
	}
}

// openSessionPicker lists recent sessions in this project for /resume.
func (b *Bubble) openSessionPicker() {
	sessions := listRecentSessions(b.cwd, 12)
	if len(sessions) == 0 {
		b.pushBlock(blockNotice, "no prior sessions to resume in this project")
		return
	}
	b.picker = &picker{
		title: "Resume session",
		items: sessions,
		onSelect: func(v string) tea.Cmd {
			if err := b.resumeSession(v); err != nil {
				b.pushBlock(blockNotice, "resume failed: "+err.Error())
			}
			return nil
		},
	}
}

// resumeSession loads a prior session's transcript into the live engine and
// re-points the writer at the same file so further turns continue that session.
func (b *Bubble) resumeSession(id string) error {
	p, err := transcript.FindSession(b.cwd, id)
	if err != nil {
		return err
	}
	res, err := transcript.Load(p)
	if err != nil {
		return err
	}
	b.engine.Messages = res.Messages
	if tw, err := transcript.New(b.cwd, id); err == nil {
		tw.SetParent(res.LastUUID)
		b.engine.Transcript = tw
	}
	b.blocks = nil
	b.curAsst = -1
	label := id
	if len(label) > 8 {
		label = label[:8]
	}
	b.pushBlock(blockNotice, fmt.Sprintf("resumed session %s — %d messages loaded", label, len(res.Messages)))
	b.refresh()
	return nil
}

// listRecentSessions returns up to `limit` sessions for cwd's project, newest
// first, labeled with id + relative age.
func listRecentSessions(cwd string, limit int) []pickerItem {
	dir, err := transcript.ProjectDir(cwd)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type se struct {
		id  string
		mod time.Time
	}
	var ss []se
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		ss = append(ss, se{id: strings.TrimSuffix(e.Name(), ".jsonl"), mod: info.ModTime()})
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].mod.After(ss[j].mod) })
	if len(ss) > limit {
		ss = ss[:limit]
	}
	out := make([]pickerItem, 0, len(ss))
	for _, s := range ss {
		label := transcript.FirstPrompt(filepath.Join(dir, s.id+".jsonl"))
		if label == "" {
			label = s.id
		}
		if len(label) > 60 {
			label = label[:59] + "…"
		}
		short := s.id
		if len(short) > 8 {
			short = short[:8]
		}
		out = append(out, pickerItem{label: label, desc: short + " · " + humanAge(s.mod), value: s.id})
	}
	return out
}

func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h ago"
	default:
		return itoa(int(d.Hours()/24)) + "d ago"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
