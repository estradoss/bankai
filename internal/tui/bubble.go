package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
}

type askState struct {
	req   permission.Request
	reply chan permission.Decision
}

// tea messages
type streamMsg string
type doneMsg struct{ err error }
type askMsg struct {
	req   permission.Request
	reply chan permission.Decision
}

var (
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	modalStyle  = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("11")).
			Padding(0, 1)
)

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
