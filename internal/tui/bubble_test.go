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
	if !strings.Contains(f, "test-model") || !strings.Contains(f, "perms=default") {
		t.Fatalf("footer = %q", f)
	}
}
