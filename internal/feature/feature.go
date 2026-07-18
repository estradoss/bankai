// Package feature is a runtime feature-flag registry — the Go analogue of
// vibelearn's compile-time feature('FLAG') system (scripts/build.ts). Instead of
// bundling variants at build time, bankai resolves a flag set at startup from
// build defaults, the BANKAI_FEATURES env var, and --feature CLI flags, then
// gates optional subsystems on feature.Enabled("FLAG").
package feature

import (
	"sort"
	"strings"
)

// Defaults is the built-in flag set. Subsystems default on; experimental /
// external-dependency features default off, matching vibelearn's split.
var Defaults = map[string]bool{
	"SKILLS":  true,
	"MCP":     true,
	"LSP":     true,
	"MEMORY":  true,
	"PLUGINS": true,
	"TASKS":   true,
	"TUI":     true,
	// Off by default (need external backends / not yet ported):
	"VOICE_MODE":  false,
	"BRIDGE_MODE": false,
	"BEDROCK":     false,
	"VERTEX":      false,
	"REMOTE":      false,
}

// Set is a resolved flag set.
type Set struct {
	flags map[string]bool
}

// Enabled reports whether a flag is on. Unknown flags are off.
func (s *Set) Enabled(name string) bool { return s.flags[normalize(name)] }

// List returns the flags sorted, each as "FLAG=on|off".
func (s *Set) List() []string {
	names := make([]string, 0, len(s.flags))
	for n := range s.flags {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, n := range names {
		state := "off"
		if s.flags[n] {
			state = "on"
		}
		out[i] = n + "=" + state
	}
	return out
}

// Resolve builds a Set from the defaults, an env string (BANKAI_FEATURES), and
// any --feature CLI overrides. Precedence: defaults < env < CLI.
//
// Token forms (comma- or space-separated in env; one per --feature flag):
//
//	FLAG or +FLAG       enable
//	-FLAG               disable
//	FLAG=0 / FLAG=false disable; FLAG=1 / FLAG=true enable
func Resolve(env string, cli []string) *Set {
	flags := map[string]bool{}
	for k, v := range Defaults {
		flags[k] = v
	}
	apply := func(tokens []string) {
		for _, tok := range tokens {
			name, on, ok := parseToken(tok)
			if !ok {
				continue
			}
			flags[name] = on
		}
	}
	apply(splitEnv(env))
	apply(cli)
	return &Set{flags: flags}
}

func splitEnv(env string) []string {
	if strings.TrimSpace(env) == "" {
		return nil
	}
	return strings.FieldsFunc(env, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

func parseToken(tok string) (name string, on bool, ok bool) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return "", false, false
	}
	if eq := strings.IndexByte(tok, '='); eq >= 0 {
		name = normalize(tok[:eq])
		val := strings.ToLower(strings.TrimSpace(tok[eq+1:]))
		return name, val == "1" || val == "true" || val == "on" || val == "yes", name != ""
	}
	switch tok[0] {
	case '-':
		return normalize(tok[1:]), false, len(tok) > 1
	case '+':
		return normalize(tok[1:]), true, len(tok) > 1
	default:
		return normalize(tok), true, true
	}
}

func normalize(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}
