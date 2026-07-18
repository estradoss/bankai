package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/estradoss/bankai/internal/commands"
	"github.com/estradoss/bankai/internal/engine"
	"github.com/estradoss/bankai/internal/goal"
	"github.com/estradoss/bankai/internal/permission"
	"github.com/estradoss/bankai/internal/provider"
)

func newTestBubble(t *testing.T) *Bubble {
	t.Helper()
	e := &engine.Engine{Client: &provider.Client{Model: "test-model"}}
	e.Perms = permission.New(permission.ModeDefault, nil, nil)
	g := goal.NewStore(t.TempDir())
	b := NewBubble(context.Background(), e, commands.NewRegistry(), g)
	// Size it so the viewport is ready.
	m, _ := b.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m.(*Bubble)
}

func TestBubbleStreamAppends(t *testing.T) {
	b := newTestBubble(t)
	if !b.ready {
		t.Fatal("should be ready after WindowSizeMsg")
	}
	m, _ := b.Update(streamMsg("hello world"))
	b = m.(*Bubble)
	if !strings.Contains(b.content, "hello world") {
		t.Fatalf("content missing stream: %q", b.content)
	}
}

func TestBubbleAskModalRoundTrip(t *testing.T) {
	b := newTestBubble(t)
	reply := make(chan permission.Decision, 1)
	m, _ := b.Update(askMsg{req: permission.Request{Tool: "Bash"}, reply: reply})
	b = m.(*Bubble)
	if b.asking == nil {
		t.Fatal("asking state should be set")
	}
	// View should render the modal, not the input.
	if !strings.Contains(b.View(), "Allow Bash?") {
		t.Fatalf("modal not rendered: %s", b.View())
	}
	// Press 'a' -> allow always.
	m, _ = b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	b = m.(*Bubble)
	if b.asking != nil {
		t.Fatal("asking should clear after answer")
	}
	select {
	case d := <-reply:
		if d != permission.DecideAllowAlways {
			t.Fatalf("decision = %v", d)
		}
	default:
		t.Fatal("no decision sent to reply channel")
	}
}

func TestBubbleUnknownCommand(t *testing.T) {
	b := newTestBubble(t)
	b.submit("/nope")
	if !strings.Contains(b.content, "unknown command") {
		t.Fatalf("expected unknown-command notice: %q", b.content)
	}
}

func TestBubbleFooterShowsModelAndPerms(t *testing.T) {
	b := newTestBubble(t)
	f := b.footer()
	if !strings.Contains(f, "test-model") || !strings.Contains(f, "default") {
		t.Fatalf("footer = %q", f)
	}
	if !strings.Contains(f, "shift+tab to cycle") {
		t.Fatalf("footer missing perms hint: %q", f)
	}
}

// fakeCmd is a minimal command for autocomplete tests.
type fakeCmd struct{ name, desc string }

func (c fakeCmd) Name() string        { return c.name }
func (c fakeCmd) Description() string { return c.desc }
func (c fakeCmd) Run(commands.Context, string) (commands.Result, error) {
	return commands.Result{}, nil
}

func newTestBubbleWithCmds(t *testing.T, cs ...commands.Command) *Bubble {
	t.Helper()
	e := &engine.Engine{Client: &provider.Client{Model: "test-model"}}
	e.Perms = permission.New(permission.ModeDefault, nil, nil)
	reg := commands.NewRegistry()
	for _, c := range cs {
		reg.Register(c)
	}
	b := NewBubble(context.Background(), e, reg, goal.NewStore(t.TempDir()))
	m, _ := b.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m.(*Bubble)
}

func typeStr(b *Bubble, s string) *Bubble {
	for _, r := range s {
		m, _ := b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		b = m.(*Bubble)
	}
	return b
}

func TestBubbleAutocompletePopup(t *testing.T) {
	b := newTestBubbleWithCmds(t,
		fakeCmd{"model", "switch model"},
		fakeCmd{"memory", "manage memory"},
		fakeCmd{"help", "list commands"},
	)
	b = typeStr(b, "/m")
	if len(b.sugg) != 2 { // memory, model
		t.Fatalf("expected 2 matches for /m, got %d", len(b.sugg))
	}
	// Tab accepts the highlighted one and fills the input.
	m, _ := b.Update(tea.KeyMsg{Type: tea.KeyTab})
	b = m.(*Bubble)
	if !strings.HasPrefix(b.input.Value(), "/m") || len(b.sugg) != 0 {
		t.Fatalf("accept failed: value=%q sugg=%d", b.input.Value(), len(b.sugg))
	}
}

func TestBubbleShiftTabCyclesPerms(t *testing.T) {
	b := newTestBubble(t)
	start := b.engine.Perms.Mode()
	m, _ := b.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	b = m.(*Bubble)
	if b.engine.Perms.Mode() == start {
		t.Fatalf("shift+tab did not change mode from %s", start)
	}
}

func TestBubbleCtrlCArmsThenExits(t *testing.T) {
	b := newTestBubble(t)
	// First ctrl+c at idle arms the exit hint.
	m, _ := b.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	b = m.(*Bubble)
	if !b.armed {
		t.Fatal("first ctrl+c should arm exit")
	}
	// Second ctrl+c returns tea.Quit.
	_, cmd := b.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("second ctrl+c should quit")
	}
}
