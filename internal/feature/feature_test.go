package feature

import (
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	s := Resolve("", nil)
	if !s.Enabled("MCP") || !s.Enabled("MEMORY") {
		t.Fatal("subsystems should default on")
	}
	if s.Enabled("VOICE_MODE") {
		t.Fatal("VOICE_MODE should default off")
	}
	if s.Enabled("UNKNOWN_FLAG") {
		t.Fatal("unknown flag should be off")
	}
}

func TestEnvOverrides(t *testing.T) {
	s := Resolve("-MCP, VOICE_MODE, LSP=0", nil)
	if s.Enabled("MCP") {
		t.Fatal("-MCP should disable")
	}
	if !s.Enabled("VOICE_MODE") {
		t.Fatal("VOICE_MODE token should enable")
	}
	if s.Enabled("LSP") {
		t.Fatal("LSP=0 should disable")
	}
}

func TestCLIBeatsEnv(t *testing.T) {
	// env disables MCP, CLI re-enables it.
	s := Resolve("-MCP", []string{"MCP"})
	if !s.Enabled("MCP") {
		t.Fatal("CLI should override env")
	}
}

func TestCaseInsensitive(t *testing.T) {
	s := Resolve("-mcp", nil)
	if s.Enabled("MCP") {
		t.Fatal("lowercase token should disable MCP")
	}
}

func TestList(t *testing.T) {
	s := Resolve("-MCP", nil)
	joined := strings.Join(s.List(), " ")
	if !strings.Contains(joined, "MCP=off") || !strings.Contains(joined, "MEMORY=on") {
		t.Fatalf("list = %q", joined)
	}
}

func TestPlusPrefix(t *testing.T) {
	s := Resolve("+BEDROCK", nil)
	if !s.Enabled("BEDROCK") {
		t.Fatal("+BEDROCK should enable")
	}
}
