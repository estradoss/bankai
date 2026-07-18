package memory

import (
	"regexp"
	"strings"
)

// secretPattern is a named detector for a class of credential.
type secretPattern struct {
	name string
	re   *regexp.Regexp
}

// secretPatterns covers the common high-confidence credential shapes. These are
// deliberately specific to keep false positives low — the scanner runs before a
// memory is persisted, and blocking a legitimate save is worse than missing an
// exotic secret.
var secretPatterns = []secretPattern{
	{"AWS access key ID", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\bgh[pousr]_[0-9A-Za-z]{36,}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}\b`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}\b`)},
	{"OpenAI/Anthropic key", regexp.MustCompile(`\b(?:sk|pk)-[A-Za-z0-9_\-]{20,}\b`)},
	{"Stripe secret key", regexp.MustCompile(`\b(?:sk|rk)_live_[0-9A-Za-z]{16,}\b`)},
	{"JWT", regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b`)},
	{"private key block", regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`)},
	{"generic secret assignment", regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|password|passwd|token|access[_-]?key)\b\s*[:=]\s*['"]?[A-Za-z0-9/\+_\-]{16,}['"]?`)},
}

// ScanSecrets returns the names of any credential patterns found in text.
// Duplicates are collapsed; order follows secretPatterns.
func ScanSecrets(text string) []string {
	var found []string
	seen := map[string]bool{}
	for _, p := range secretPatterns {
		if p.re.MatchString(text) && !seen[p.name] {
			seen[p.name] = true
			found = append(found, p.name)
		}
	}
	return found
}

// SecretError formats the scanner hits into a human-readable refusal message.
func SecretError(hits []string) string {
	return "refusing to save: content appears to contain " + strings.Join(hits, ", ") +
		". Memories are stored in plaintext — remove the secret (or reference it indirectly) and try again."
}
