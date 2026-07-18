package permission

import (
	"encoding/json"
	"testing"
)

func eval(g *Gate, tool string) Behavior { return g.Evaluate(tool, json.RawMessage(`{}`)) }

func TestModeDefaults(t *testing.T) {
	cases := []struct {
		mode Mode
		tool string
		want Behavior
	}{
		{ModeDefault, "Read", Allow},
		{ModeDefault, "Edit", Ask},
		{ModeDefault, "Bash", Ask},
		{ModeAcceptEdits, "Edit", Allow},
		{ModeAcceptEdits, "Bash", Ask},
		{ModeBypass, "Bash", Allow},
		{ModeDontAsk, "Edit", Allow},
		{ModePlan, "Read", Allow},
		{ModePlan, "Edit", Deny},
		{ModePlan, "Write", Deny},
		{ModePlan, "Task", Deny},
		{ModePlan, "Bash", Ask},
		{ModeDefault, "UnknownTool", Ask}, // fail-closed as exec
	}
	for _, c := range cases {
		g := New(c.mode, nil, nil)
		if got := eval(g, c.tool); got != c.want {
			t.Errorf("mode=%s tool=%s: got %s want %s", c.mode, c.tool, got, c.want)
		}
	}
}

func TestDenyBeatsAllow(t *testing.T) {
	g := New(ModeBypass, []Rule{{ToolName: "Bash", Behavior: Allow}}, []Rule{{ToolName: "Bash", Behavior: Deny}})
	if got := eval(g, "Bash"); got != Deny {
		t.Fatalf("deny should win: got %s", got)
	}
}

func TestAllowRuleOverridesAsk(t *testing.T) {
	g := New(ModeDefault, []Rule{{ToolName: "Edit", Behavior: Allow}}, nil)
	if got := eval(g, "Edit"); got != Allow {
		t.Fatalf("allow rule should override ask: got %s", got)
	}
}

func TestRuleContentMatch(t *testing.T) {
	g := New(ModeDefault, nil, []Rule{{ToolName: "Bash", Match: "rm -rf", Behavior: Deny}})
	if got := g.Evaluate("Bash", json.RawMessage(`{"command":"rm -rf /"}`)); got != Deny {
		t.Fatalf("content-matched deny: got %s", got)
	}
	if got := g.Evaluate("Bash", json.RawMessage(`{"command":"ls"}`)); got != Ask {
		t.Fatalf("non-matching should fall through to ask: got %s", got)
	}
}

func TestCheckAskerAllowAlwaysAddsRule(t *testing.T) {
	g := New(ModeDefault, nil, nil)
	calls := 0
	g.Asker = func(Request) Decision { calls++; return DecideAllowAlways }
	if ok, _ := g.Check("Edit", json.RawMessage(`{}`)); !ok {
		t.Fatal("first Edit should be allowed")
	}
	if ok, _ := g.Check("Edit", json.RawMessage(`{}`)); !ok {
		t.Fatal("second Edit should be allowed")
	}
	if calls != 1 {
		t.Fatalf("asker should be consulted once (session rule added), got %d", calls)
	}
}

func TestCheckNilAskerFailsClosed(t *testing.T) {
	g := New(ModeDefault, nil, nil)
	if ok, reason := g.Check("Bash", json.RawMessage(`{}`)); ok || reason == "" {
		t.Fatalf("nil asker must deny with reason, got ok=%v reason=%q", ok, reason)
	}
}

func TestWildcardRule(t *testing.T) {
	g := New(ModeBypass, nil, []Rule{{ToolName: "*", Behavior: Deny}})
	if got := eval(g, "Read"); got != Deny {
		t.Fatalf("wildcard deny should hit any tool: got %s", got)
	}
}
