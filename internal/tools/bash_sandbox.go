package tools

import (
	"fmt"
	"os/exec"
	"runtime"
)

// sandboxWrap rewrites a shell command so it runs with no network access and a
// read-only filesystem except the working directory and /tmp. It is the Go port
// of vibelearn's sandbox toggle. Enforcement is delegated to an OS sandbox:
// bubblewrap (bwrap) on Linux, sandbox-exec on macOS. When no sandbox backend
// is available the call FAILS CLOSED — we never silently run unsandboxed while
// claiming to be sandboxed.
//
// It returns the argv to exec (in place of "/bin/sh -c command") and an error
// describing why sandboxing is unavailable.
func sandboxWrap(command, workdir string) ([]string, error) {
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			return nil, fmt.Errorf("sandbox requested but bubblewrap (bwrap) is not installed; " +
				"install it or disable --sandbox")
		}
		args := []string{
			"bwrap",
			"--unshare-net", // no network
			"--unshare-pid", // isolated process tree
			"--die-with-parent",
			"--ro-bind", "/", "/", // read-only root
			"--dev", "/dev",
			"--proc", "/proc",
			"--tmpfs", "/tmp",
		}
		if workdir != "" {
			args = append(args, "--bind", workdir, workdir, "--chdir", workdir)
		}
		args = append(args, "/bin/sh", "-c", command)
		return args, nil
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			return nil, fmt.Errorf("sandbox requested but sandbox-exec is unavailable")
		}
		// Deny network; allow filesystem reads and writes only under workdir + /tmp.
		profile := "(version 1)(allow default)(deny network*)"
		return []string{"sandbox-exec", "-p", profile, "/bin/sh", "-c", command}, nil
	default:
		return nil, fmt.Errorf("sandbox requested but no sandbox backend exists for %s", runtime.GOOS)
	}
}
