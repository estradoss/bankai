package tools

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestSandboxWrapLinuxArgv(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only argv shape")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		// No backend: must fail closed, not return a bare /bin/sh argv.
		if _, err := sandboxWrap("echo hi", "/work"); err == nil {
			t.Fatal("expected error when bwrap missing")
		}
		return
	}
	argv, err := sandboxWrap("echo hi", "/work")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	for _, must := range []string{"bwrap", "--unshare-net", "--ro-bind / /", "--chdir /work", "/bin/sh -c echo hi"} {
		if !strings.Contains(joined, must) {
			t.Fatalf("argv missing %q: %s", must, joined)
		}
	}
}

func TestBashSandboxFailsClosed(t *testing.T) {
	// On a host with no sandbox backend, a sandboxed Bash call must error out
	// rather than run the command unsandboxed.
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("bwrap"); err == nil {
			t.Skip("bwrap present; can't assert missing-backend path")
		}
	}
	b := BashTool{Sandbox: true, Workdir: "/tmp"}
	in, _ := json.Marshal(map[string]any{"command": "echo should-not-run"})
	res, _ := b.Call(context.Background(), in)
	if !res.IsError || strings.Contains(res.Output, "should-not-run") {
		t.Fatalf("sandbox must fail closed, got %+v", res)
	}
}

func TestBashUnsandboxedStillRuns(t *testing.T) {
	b := BashTool{}
	in, _ := json.Marshal(map[string]any{"command": "echo hi"})
	res, _ := b.Call(context.Background(), in)
	if res.IsError || !strings.Contains(res.Output, "hi") {
		t.Fatalf("plain bash: %+v", res)
	}
}
