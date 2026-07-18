package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseRule(t *testing.T) {
	r, ok := parseRule("Read", Allow)
	if !ok || r.ToolName != "Read" || r.Match != "" {
		t.Fatalf("plain: %+v ok=%v", r, ok)
	}
	r, _ = parseRule("Bash(git:*)", Deny)
	if r.ToolName != "Bash" || r.Match != "git" || r.Behavior != Deny {
		t.Fatalf("glob: %+v", r)
	}
	r, _ = parseRule("Read(/etc/**)", Allow)
	if r.ToolName != "Read" || r.Match != "/etc/" {
		t.Fatalf("path glob: %+v", r)
	}
	if _, ok := parseRule("   ", Allow); ok {
		t.Fatal("blank rule should be rejected")
	}
}

func writeSettings(t *testing.T, dir, name string, v any) {
	t.Helper()
	p := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	if err := os.WriteFile(filepath.Join(p, name), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSettings(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	writeSettings(t, home, "settings.json", map[string]any{
		"permissions": map[string]any{
			"allow": []string{"Read", "Bash(ls:*)"},
		},
	})
	writeSettings(t, proj, "settings.json", map[string]any{
		"permissions": map[string]any{
			"deny":        []string{"Bash(rm:*)"},
			"defaultMode": "acceptEdits",
		},
	})

	allow, deny, mode := LoadSettings(home, proj)
	if len(allow) != 2 || len(deny) != 1 {
		t.Fatalf("rule counts: allow=%d deny=%d", len(allow), len(deny))
	}
	if mode != ModeAcceptEdits {
		t.Fatalf("mode = %s", mode)
	}

	// Verify the loaded rules actually gate: deny rm, allow ls under default.
	g := New(ModeDefault, allow, deny)
	if got := g.Evaluate("Bash", json.RawMessage(`{"command":"rm -rf /"}`)); got != Deny {
		t.Fatalf("rm should be denied, got %s", got)
	}
	if got := g.Evaluate("Bash", json.RawMessage(`{"command":"ls -la"}`)); got != Allow {
		t.Fatalf("ls should be allowed, got %s", got)
	}
}

func TestLoadSettingsMissing(t *testing.T) {
	allow, deny, mode := LoadSettings(t.TempDir(), t.TempDir())
	if len(allow) != 0 || len(deny) != 0 || mode != "" {
		t.Fatalf("missing settings should yield nothing: %d %d %q", len(allow), len(deny), mode)
	}
}
