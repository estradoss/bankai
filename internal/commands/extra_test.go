package commands

import (
	"strings"
	"testing"

	"github.com/estradoss/bankai/internal/engine"
	"github.com/estradoss/bankai/internal/tools"
)

func TestToolsAndSystemCommands(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.ReadTool{})
	reg.Register(tools.BashTool{})
	eng := &engine.Engine{Tools: reg, System: "SYSTEM-PROMPT-MARKER"}
	ctx := Context{Engine: eng}

	res, err := Tools{}.Run(ctx, "")
	if err != nil || !strings.Contains(res.Text, "Bash") || !strings.Contains(res.Text, "Read") {
		t.Fatalf("tools: %q err=%v", res.Text, err)
	}

	res, err = System{}.Run(ctx, "")
	if err != nil || !strings.Contains(res.Text, "SYSTEM-PROMPT-MARKER") {
		t.Fatalf("system: %q err=%v", res.Text, err)
	}
}

func TestPWD(t *testing.T) {
	res, err := PWD{}.Run(Context{}, "")
	if err != nil || res.Text == "" {
		t.Fatalf("pwd: %q err=%v", res.Text, err)
	}
}
