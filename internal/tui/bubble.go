package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/estradoss/bankai/internal/theme"

	"github.com/estradoss/bankai/internal/commands"
	"github.com/estradoss/bankai/internal/engine"
	"github.com/estradoss/bankai/internal/goal"
	"github.com/estradoss/bankai/internal/permission"
)

// Bubble is the Bubbletea-based TUI: a scrollback viewport, a prompt input, a
// thinking spinner, a goal/model footer, and a modal permission prompt. It is
// the rich-renderer port of vibelearn's Ink UI (src/ink/). The line-based REPL
// remains available as a fallback (see REPL).
type Bubble struct {
	engine *engine.Engine
	cmds   *commands.Registry
	goals  *goal.Store
	ctx    context.Context

	vp    viewport.Model
	input textinput.Model
	spin  spinner.Model

	content string // accumulated transcript markdown
	busy    bool
	asking  *askState
	err     string

	width, height int
	ready         bool

	// Vim modal editing. vimOn enables it; vimNormal true = normal mode, false =
	// insert mode. Ported from vibelearn's vim input mode.
	vimOn     bool
	vimNormal bool

	// Slash-command autocomplete popup: shown while the input is a bare "/foo"
	// with no space yet. sugg holds the filtered commands, suggIdx the highlight.
	sugg    []commands.Command
	suggIdx int

	// turnCancel cancels the in-flight engine turn (ctrl+c to interrupt). armed
	// tracks the "press ctrl+c again to exit" idle window.
	turnCancel context.CancelFunc
	armed      bool

	// Banner metadata for the welcome header.
	version, user, cwd, effort string
}

type askState struct {
	req   permission.Request
	reply chan permission.Decision
}

// tea messages
type streamMsg string
type toolMsg struct{ name, input string }
type doneMsg struct{ err error }
type askMsg struct {
	req   permission.Request
	reply chan permission.Decision
}

var (
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	toolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	modalStyle  = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("11")).
			Padding(0, 1)

	accentColor = lipgloss.Color("6")
	borderColor = lipgloss.Color("240")

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	headerBar   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	inputBox    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)
	toolBox = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("5")).
		Foreground(lipgloss.Color("5")).
		PaddingLeft(1)
	suggBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")).
		Padding(0, 1)
	suggSel = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	suggDim = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// ApplyTheme repoints the TUI styles at a palette. Call before constructing the
// Bubble model. Ported from vibelearn's themeable TUI.
func ApplyTheme(p theme.Palette) {
	footerStyle = footerStyle.Foreground(lipgloss.Color(p.Footer))
	userStyle = userStyle.Foreground(lipgloss.Color(p.Accent))
	errStyle = errStyle.Foreground(lipgloss.Color(p.Error))
	toolStyle = toolStyle.Foreground(lipgloss.Color(p.Tool))
	modalStyle = modalStyle.BorderForeground(lipgloss.Color(p.Border))

	accentColor = lipgloss.Color(p.Accent)
	borderColor = lipgloss.Color(p.Border)
	headerStyle = headerStyle.Foreground(lipgloss.Color(p.Accent))
	headerBar = headerBar.Foreground(lipgloss.Color(p.Footer))
	inputBox = inputBox.BorderForeground(lipgloss.Color(p.Border))
	toolBox = toolBox.BorderForeground(lipgloss.Color(p.Tool)).Foreground(lipgloss.Color(p.Tool))
	suggBox = suggBox.BorderForeground(lipgloss.Color(p.Accent))
	suggSel = suggSel.Foreground(lipgloss.Color(p.Accent))
	suggDim = suggDim.Foreground(lipgloss.Color(p.Footer))
}

// BannerInfo carries the welcome-header metadata.
type BannerInfo struct {
	Version string
	User    string
	Cwd     string
	Effort  string
}

// NewBubble constructs the Bubbletea TUI model.
func NewBubble(ctx context.Context, e *engine.Engine, c *commands.Registry, g *goal.Store) *Bubble {
	return NewBubbleWithBanner(ctx, e, c, g, BannerInfo{})
}

// NewBubbleWithBanner constructs the TUI with welcome-banner metadata.
func NewBubbleWithBanner(ctx context.Context, e *engine.Engine, c *commands.Registry, g *goal.Store, info BannerInfo) *Bubble {
	ti := textinput.New()
	ti.Placeholder = "message, or / for commands"
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 0

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	b := &Bubble{engine: e, cmds: c, goals: g, ctx: ctx, input: ti, spin: sp,
		version: info.Version, user: info.User, cwd: info.Cwd, effort: info.Effort}
	b.content = b.banner()
	return b
}

// mascot is a small ascii critter for the welcome banner.
const mascot = "  ╭─────╮\n  │ ● ● │\n  ╰──┬──╯\n   bankai"

// banner renders the welcome header shown at the top of the scrollback.
func (b *Bubble) banner() string {
	title := "bankai"
	if b.version != "" {
		title += " v" + b.version
	}
	who := "Welcome" + func() string {
		if b.user != "" {
			return " back " + b.user
		}
		return ""
	}() + "!"

	model := b.engine.Client.Model
	if b.effort != "" {
		model += " · " + b.effort + " effort"
	}
	left := []string{
		headerStyle.Render(who),
		"",
		suggDim.Render(mascot),
		"",
		suggDim.Render(model),
	}
	if b.cwd != "" {
		left = append(left, suggDim.Render(b.cwd))
	}
	tips := lipgloss.NewStyle().Foreground(accentColor).Render("Tips") + "\n" +
		suggDim.Render("· /init to seed a memory file\n· /help for all commands\n· shift+tab cycles perms")

	leftBox := lipgloss.NewStyle().Padding(0, 2).Render(strings.Join(left, "\n"))
	rightBox := lipgloss.NewStyle().Padding(0, 2).Render(tips)
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftBox, rightBox)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Render(headerStyle.Render(title) + "\n" + body)
	return box + "\n"
}

func (b *Bubble) Init() tea.Cmd { return textinput.Blink }

// program is the running tea program, set by Run so callbacks can Send into it.
func (b *Bubble) Run() error {
	p := tea.NewProgram(b, tea.WithAltScreen(), tea.WithContext(b.ctx))

	b.engine.OnText = func(chunk string) { p.Send(streamMsg(chunk)) }
	b.engine.OnToolStart = func(name string, input json.RawMessage) {
		in := string(input)
		if len(in) > 160 {
			in = in[:157] + "..."
		}
		p.Send(toolMsg{name: name, input: in})
	}
	if b.engine.Perms != nil {
		b.engine.Perms.Asker = func(req permission.Request) permission.Decision {
			reply := make(chan permission.Decision, 1)
			p.Send(askMsg{req: req, reply: reply})
			return <-reply
		}
	}
	_, err := p.Run()
	return err
}

func (b *Bubble) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		b.width, b.height = msg.Width, msg.Height
		vpHeight := msg.Height - 6 // reserve boxed input (3) + footer (2) + margin
		if vpHeight < 3 {
			vpHeight = 3
		}
		if !b.ready {
			b.vp = viewport.New(msg.Width, vpHeight)
			b.ready = true
		} else {
			b.vp.Width = msg.Width
			b.vp.Height = vpHeight
		}
		b.input.Width = msg.Width - 3
		b.refresh()

	case tea.KeyMsg:
		if b.asking != nil {
			return b, b.handleAsk(msg)
		}
		// Slash-command autocomplete navigation takes priority when the popup
		// is open: ↑/↓ or ctrl+p/n move; tab/→ accept; esc dismisses.
		if len(b.sugg) > 0 {
			switch msg.String() {
			case "up", "ctrl+p":
				b.suggIdx = (b.suggIdx - 1 + len(b.sugg)) % len(b.sugg)
				return b, nil
			case "down", "ctrl+n":
				b.suggIdx = (b.suggIdx + 1) % len(b.sugg)
				return b, nil
			case "tab", "right":
				b.acceptSuggestion()
				return b, nil
			case "esc":
				b.sugg = nil
				return b, nil
			}
		}
		if b.vimOn {
			if cmd, consumed := b.handleVim(msg); consumed {
				b.updateSuggestions()
				return b, cmd
			}
		}
		// shift+tab cycles the permission mode (like vibelearn).
		if msg.String() == "shift+tab" {
			b.cyclePerms()
			return b, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			// While a turn runs, ctrl+c interrupts it. At idle, the first
			// ctrl+c arms and the second quits.
			if b.busy && b.turnCancel != nil {
				b.turnCancel()
				b.content += errStyle.Render("⎯ interrupted") + "\n"
				b.refresh()
				return b, nil
			}
			if b.armed {
				return b, tea.Quit
			}
			b.armed = true
			b.err = "press ctrl+c again to exit"
			return b, nil
		case tea.KeyEnter:
			if b.busy {
				break
			}
			b.armed = false
			line := strings.TrimSpace(b.input.Value())
			if line != "" {
				b.input.Reset()
				b.sugg = nil
				if cmd := b.submit(line); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		default:
			b.armed = false
		}

	case streamMsg:
		b.content += string(msg)
		b.refresh()

	case toolMsg:
		// Tool-call panel: a left-bordered, colored block showing the call.
		panel := toolBox.Render(fmt.Sprintf("⚙ %s %s", msg.name, msg.input))
		b.content += panel + "\n"
		b.refresh()

	case doneMsg:
		b.busy = false
		b.turnCancel = nil
		if msg.err != nil && !strings.Contains(msg.err.Error(), "context canceled") {
			b.err = msg.err.Error()
		} else {
			b.err = ""
		}
		b.content += "\n"
		b.refresh()

	case askMsg:
		b.asking = &askState{req: msg.req, reply: msg.reply}

	case spinner.TickMsg:
		if b.busy {
			var cmd tea.Cmd
			b.spin, cmd = b.spin.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	var icmd, vcmd tea.Cmd
	b.input, icmd = b.input.Update(msg)
	b.vp, vcmd = b.vp.Update(msg)
	cmds = append(cmds, icmd, vcmd)
	// Recompute the slash-command popup from the (possibly changed) input.
	if _, isKey := msg.(tea.KeyMsg); isKey {
		b.updateSuggestions()
	}
	return b, tea.Batch(cmds...)
}

// updateSuggestions refreshes the autocomplete popup: it shows the matching
// commands whenever the input is a bare "/prefix" (no space yet), else clears.
func (b *Bubble) updateSuggestions() {
	val := b.input.Value()
	if !strings.HasPrefix(val, "/") || strings.ContainsAny(val, " \t") {
		b.sugg = nil
		return
	}
	prefix := strings.ToLower(strings.TrimPrefix(val, "/"))
	var matches []commands.Command
	for _, c := range b.cmds.List() {
		if strings.HasPrefix(c.Name(), prefix) {
			matches = append(matches, c)
		}
	}
	b.sugg = matches
	if b.suggIdx >= len(matches) {
		b.suggIdx = 0
	}
}

// acceptSuggestion fills the input with the highlighted command name.
func (b *Bubble) acceptSuggestion() {
	if b.suggIdx < 0 || b.suggIdx >= len(b.sugg) {
		return
	}
	name := b.sugg[b.suggIdx].Name()
	b.input.SetValue("/" + name + " ")
	b.input.CursorEnd()
	b.sugg = nil
}

// renderSuggestions draws the autocomplete popup (max ~8 rows).
func (b *Bubble) renderSuggestions() string {
	const maxRows = 8
	n := len(b.sugg)
	start := 0
	if b.suggIdx >= maxRows {
		start = b.suggIdx - maxRows + 1
	}
	end := start + maxRows
	if end > n {
		end = n
	}
	var rows []string
	for i := start; i < end; i++ {
		c := b.sugg[i]
		name := "/" + c.Name()
		desc := c.Description()
		if len(desc) > 54 {
			desc = desc[:53] + "…"
		}
		line := fmt.Sprintf("%-16s %s", name, suggDim.Render(desc))
		if i == b.suggIdx {
			line = suggSel.Render("▸ ") + suggSel.Render(fmt.Sprintf("%-16s", name)) + " " + suggDim.Render(desc)
		} else {
			line = "  " + line
		}
		rows = append(rows, line)
	}
	if n > maxRows {
		rows = append(rows, suggDim.Render(fmt.Sprintf("  … %d more · ↑/↓ to move · tab to accept", n-maxRows)))
	}
	return suggBox.Width(b.width - 2).Render(strings.Join(rows, "\n"))
}

// submit handles a line: either a slash command or a model turn.
func (b *Bubble) submit(line string) tea.Cmd {
	b.err = ""
	if name, args, ok := commands.Parse(line); ok {
		cmd, found := b.cmds.Get(name)
		if !found {
			b.appendUser(line)
			b.content += errStyle.Render("unknown command: /"+name) + "\n"
			b.refresh()
			return nil
		}
		res, err := cmd.Run(commands.Context{Ctx: b.ctx, Engine: b.engine, Goals: b.goals}, args)
		if err != nil {
			b.err = err.Error()
			b.refresh()
			return nil
		}
		if res.Exit {
			return tea.Quit
		}
		if res.Text != "" {
			b.content += res.Text + "\n"
			b.refresh()
		}
		if res.Submit == "" {
			return nil
		}
		line = res.Submit
	}

	b.appendUser(line)
	b.busy = true
	return tea.Batch(b.spin.Tick, b.runTurn(line))
}

// runTurn runs one engine turn in a goroutine, reporting completion via doneMsg.
// The turn gets its own cancelable context so ctrl+c can interrupt just this
// turn without tearing down the whole program.
func (b *Bubble) runTurn(line string) tea.Cmd {
	turnCtx, cancel := context.WithCancel(b.ctx)
	b.turnCancel = cancel
	return func() tea.Msg {
		err := b.engine.Submit(turnCtx, line)
		return doneMsg{err: err}
	}
}

// cyclePerms advances the permission mode: default → acceptEdits → plan →
// bypassPermissions → default.
func (b *Bubble) cyclePerms() {
	if b.engine.Perms == nil {
		return
	}
	order := []permission.Mode{permission.ModeDefault, permission.ModeAcceptEdits, permission.ModePlan, permission.ModeBypass}
	cur := b.engine.Perms.Mode()
	next := order[0]
	for i, m := range order {
		if m == cur {
			next = order[(i+1)%len(order)]
			break
		}
	}
	b.engine.Perms.SetMode(next)
}

// SetVim enables/disables vim modal editing (starts in normal mode).
func (b *Bubble) SetVim(on bool) {
	b.vimOn = on
	b.vimNormal = on
}

// handleVim implements a compact vim input mode. Returns (cmd, consumed): when
// consumed is true the key is fully handled and must not reach the text input.
// Enter always falls through so submission works in either mode.
func (b *Bubble) handleVim(msg tea.KeyMsg) (tea.Cmd, bool) {
	if msg.Type == tea.KeyEnter {
		return nil, false // let normal submit path handle it
	}
	if !b.vimNormal {
		// Insert mode: ESC returns to normal; everything else is normal typing.
		if msg.Type == tea.KeyEsc {
			b.vimNormal = true
			return nil, true
		}
		return nil, false
	}
	// Normal mode.
	switch msg.String() {
	case "i":
		b.vimNormal = false
	case "a":
		b.vimNormal = false
		b.input.SetCursor(b.input.Position() + 1)
	case "A":
		b.vimNormal = false
		b.input.CursorEnd()
	case "I":
		b.vimNormal = false
		b.input.CursorStart()
	case "h":
		b.input.SetCursor(b.input.Position() - 1)
	case "l":
		b.input.SetCursor(b.input.Position() + 1)
	case "0", "^":
		b.input.CursorStart()
	case "$":
		b.input.CursorEnd()
	case "x":
		v := []rune(b.input.Value())
		p := b.input.Position()
		if p >= 0 && p < len(v) {
			b.input.SetValue(string(append(v[:p], v[p+1:]...)))
			b.input.SetCursor(p)
		}
	case "d":
		// treat a lone 'd' as dd: clear the line
		b.input.SetValue("")
		b.input.CursorStart()
	}
	return nil, true // consume all other keys in normal mode
}

func (b *Bubble) handleAsk(msg tea.KeyMsg) tea.Cmd {
	var d permission.Decision
	switch strings.ToLower(msg.String()) {
	case "y":
		d = permission.DecideAllowOnce
	case "a":
		d = permission.DecideAllowAlways
	default:
		d = permission.DecideDeny
	}
	b.asking.reply <- d
	b.asking = nil
	return nil
}

func (b *Bubble) appendUser(line string) {
	b.content += userStyle.Render("› "+line) + "\n"
	b.refresh()
}

func (b *Bubble) refresh() {
	if !b.ready {
		return
	}
	b.vp.SetContent(b.content)
	b.vp.GotoBottom()
}

func (b *Bubble) View() string {
	if !b.ready {
		return "loading…"
	}
	var bottom string
	if b.asking != nil {
		in := string(b.asking.req.Input)
		if len(in) > 120 {
			in = in[:117] + "…"
		}
		bottom = modalStyle.Render(fmt.Sprintf("Allow %s?  %s\n[y]es once  [a]lways  [N]o",
			b.asking.req.Tool, in))
	} else {
		prompt := lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("› ")
		bottom = inputBox.Width(b.width - 2).Render(prompt + b.input.View())
	}
	sections := []string{b.vp.View()}
	if len(b.sugg) > 0 {
		sections = append(sections, b.renderSuggestions())
	}
	sections = append(sections, bottom, b.footer())
	return strings.Join(sections, "\n")
}

// bar renders a compact [██████░░░░] meter at the given fraction (0..1).
func bar(frac float64, width int) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*float64(width) + 0.5)
	full := lipgloss.NewStyle().Foreground(accentColor).Render(strings.Repeat("█", filled))
	empty := suggDim.Render(strings.Repeat("░", width-filled))
	return "[" + full + empty + "]"
}

func (b *Bubble) footer() string {
	// Line 1: model · effort · ctx meter · usage budget.
	seg := []string{lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(b.engine.Client.Model)}
	if b.effort != "" {
		seg = append(seg, b.effort)
	}
	// Context meter: accumulated tokens vs a ~200k soft window.
	used := b.engine.TotalUsage.InputTokens + b.engine.TotalUsage.OutputTokens
	if used > 0 {
		frac := float64(used) / 200000.0
		seg = append(seg, fmt.Sprintf("ctx %s %d%%", bar(frac, 10), int(frac*100)))
	}
	if rl := b.engine.Client.Limits.Snapshot(); rl.Seen {
		if lim, rem := atoi(rl.UnifiedLimit), atoi(rl.UnifiedRemaining); lim > 0 {
			frac := float64(lim-rem) / float64(lim)
			label := "5h"
			if rl.UnifiedReset != "" {
				label += " (" + rl.UnifiedReset + ")"
			}
			seg = append(seg, fmt.Sprintf("%s %s %d%%", label, bar(frac, 10), int(frac*100)))
		}
	}
	if g := b.goals.Get(); g != nil {
		seg = append(seg, goalLabel(g))
	}
	status := "ready"
	if b.busy {
		status = b.spin.View() + "thinking… (ctrl+c to interrupt)"
	}
	seg = append(seg, status)
	line1 := footerStyle.Render(strings.Join(seg, "  ·  "))

	// Line 2: permission mode + cycle hint.
	mode := "default"
	if b.engine.Perms != nil {
		mode = string(b.engine.Perms.Mode())
	}
	modeStyle := lipgloss.NewStyle().Foreground(accentColor).Bold(true)
	if mode == string(permission.ModeBypass) {
		modeStyle = errStyle.Bold(true)
	}
	line2 := modeStyle.Render("▶▶ "+mode) + footerStyle.Render(" (shift+tab to cycle)")

	out := line1 + "\n" + line2
	if b.err != "" {
		out += "\n" + errStyle.Render(b.err)
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
