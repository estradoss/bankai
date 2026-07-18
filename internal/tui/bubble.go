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
)

// ApplyTheme repoints the TUI styles at a palette. Call before constructing the
// Bubble model. Ported from vibelearn's themeable TUI.
func ApplyTheme(p theme.Palette) {
	footerStyle = footerStyle.Foreground(lipgloss.Color(p.Footer))
	userStyle = userStyle.Foreground(lipgloss.Color(p.Accent))
	errStyle = errStyle.Foreground(lipgloss.Color(p.Error))
	toolStyle = toolStyle.Foreground(lipgloss.Color(p.Tool))
	modalStyle = modalStyle.BorderForeground(lipgloss.Color(p.Border))
}

// NewBubble constructs the Bubbletea TUI model.
func NewBubble(ctx context.Context, e *engine.Engine, c *commands.Registry, g *goal.Store) *Bubble {
	ti := textinput.New()
	ti.Placeholder = "message, or /help"
	ti.Prompt = "› "
	ti.Focus()
	ti.CharLimit = 0

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return &Bubble{engine: e, cmds: c, goals: g, ctx: ctx, input: ti, spin: sp}
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
		vpHeight := msg.Height - 4 // reserve input + footer
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
		if b.vimOn {
			if cmd, consumed := b.handleVim(msg); consumed {
				return b, cmd
			}
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			return b, tea.Quit
		case tea.KeyEnter:
			if b.busy {
				break
			}
			line := strings.TrimSpace(b.input.Value())
			if line != "" {
				b.input.Reset()
				if cmd := b.submit(line); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}

	case streamMsg:
		b.content += string(msg)
		b.refresh()

	case toolMsg:
		// Tool-call panel: a bordered, colored line showing the call.
		panel := toolStyle.Render(fmt.Sprintf("⚙ %s %s", msg.name, msg.input))
		b.content += panel + "\n"
		b.refresh()

	case doneMsg:
		b.busy = false
		if msg.err != nil {
			b.err = msg.err.Error()
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
	return b, tea.Batch(cmds...)
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
func (b *Bubble) runTurn(line string) tea.Cmd {
	return func() tea.Msg {
		err := b.engine.Submit(b.ctx, line)
		return doneMsg{err: err}
	}
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
		bottom = b.input.View()
	}
	return strings.Join([]string{b.vp.View(), bottom, b.footer()}, "\n")
}

func (b *Bubble) footer() string {
	parts := []string{"model=" + b.engine.Client.Model}
	if b.engine.Perms != nil {
		parts = append(parts, "perms="+string(b.engine.Perms.Mode()))
	}
	if g := b.goals.Get(); g != nil {
		parts = append(parts, goalLabel(g))
	}
	if rl := b.engine.Client.Limits.Snapshot(); rl.Seen {
		switch {
		case rl.UnifiedRemaining != "":
			parts = append(parts, "budget="+rl.UnifiedRemaining)
		case rl.TokensRemaining != "":
			parts = append(parts, "tok="+rl.TokensRemaining)
		}
	}
	status := "ready"
	if b.busy {
		status = b.spin.View() + "thinking"
	}
	parts = append(parts, status)
	line := footerStyle.Render(strings.Join(parts, "  ·  "))
	if b.err != "" {
		line += "\n" + errStyle.Render("error: "+b.err)
	}
	return line
}
