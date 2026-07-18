package transcript

import (
	"os"
	"path/filepath"
	"regexp"
)

const maxSanitized = 200

var nonAlpha = regexp.MustCompile(`[^a-zA-Z0-9]`)

// SanitizePath mirrors Claude Code's project-dir encoding: every non-alphanumeric
// character becomes a dash, so `/Volumes/Home/estrada/code/bankai` →
// `-Volumes-Home-estrada-code-bankai`.
func SanitizePath(p string) string {
	s := nonAlpha.ReplaceAllString(p, "-")
	if len(s) > maxSanitized {
		s = s[:maxSanitized]
	}
	return s
}

// ProjectDir returns ~/.claude/projects/<sanitized-cwd> for the given cwd.
// It does NOT create the directory.
func ProjectDir(cwd string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects", SanitizePath(cwd)), nil
}
