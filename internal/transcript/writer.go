package transcript

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/estradoss/bankai/internal/agent"
)

const bankaiVersion = "0.1.0"

// UUID returns a random v4-ish uuid string.
func UUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[0:8], s[8:12], s[12:16], s[16:20], s[20:32])
}

// Writer appends transcript events to a Claude-Code-compatible JSONL file at
// ~/.claude/projects/<sanitized-cwd>/<sessionId>.jsonl.
type Writer struct {
	mu         sync.Mutex
	SessionID  string
	Path       string
	CWD        string
	GitBranch  string
	Version    string
	LastUUID   string // parentUuid for the next event
	permMode   string
	entrypoint string
}

// New opens (or creates) the JSONL for sessionID under cwd's project dir.
// If sessionID is empty, a new UUID is generated.
func New(cwd, sessionID string) (*Writer, error) {
	if sessionID == "" {
		sessionID = UUID()
	}
	dir, err := ProjectDir(cwd)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	w := &Writer{
		SessionID:  sessionID,
		Path:       path,
		CWD:        cwd,
		GitBranch:  gitBranch(cwd),
		Version:    bankaiVersion,
		permMode:   "default",
		entrypoint: "cli",
	}
	return w, nil
}

// SetParent forces the parentUuid for the next write. Used when resuming from
// an existing transcript (last-message uuid becomes the chain root).
func (w *Writer) SetParent(uuid string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.LastUUID = uuid
}

// WriteUser records a plain-text user prompt.
func (w *Writer) WriteUser(text string) error {
	return w.writeEnvelope("user", map[string]any{
		"role":    "user",
		"content": text,
	}, map[string]any{
		"promptId":       UUID(),
		"permissionMode": w.permMode,
	})
}

// WriteToolResults records a user-role turn whose content is a slice of
// tool_result blocks (mirrors how Claude Code stores tool round-trips).
func (w *Writer) WriteToolResults(results []agent.ContentBlock) error {
	content := make([]map[string]any, 0, len(results))
	for _, r := range results {
		content = append(content, map[string]any{
			"type":        "tool_result",
			"tool_use_id": r.ToolUseID,
			"content":     r.Content,
			"is_error":    r.IsError,
		})
	}
	return w.writeEnvelope("user", map[string]any{
		"role":    "user",
		"content": content,
	}, map[string]any{
		"permissionMode": w.permMode,
	})
}

// WriteAssistant records a full assistant message including tool_use / text /
// thinking blocks. usage may be nil.
func (w *Writer) WriteAssistant(model string, blocks []agent.ContentBlock, stopReason string, usage *agent.Usage) error {
	content := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			content = append(content, map[string]any{"type": "text", "text": b.Text})
		case "thinking":
			content = append(content, map[string]any{"type": "thinking", "thinking": b.Text})
		case "tool_use":
			var in any
			if len(b.Input) > 0 {
				_ = json.Unmarshal(b.Input, &in)
			} else {
				in = map[string]any{}
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    b.ID,
				"name":  b.Name,
				"input": in,
			})
		}
	}
	msg := map[string]any{
		"id":          "msg_" + strings.ReplaceAll(UUID(), "-", "")[:24],
		"type":        "message",
		"role":        "assistant",
		"model":       model,
		"content":     content,
		"stop_reason": stopReason,
	}
	if usage != nil {
		msg["usage"] = map[string]any{
			"input_tokens":               usage.InputTokens,
			"output_tokens":              usage.OutputTokens,
			"cache_creation_input_tokens": usage.CacheCreationInputTokens,
			"cache_read_input_tokens":    usage.CacheReadInputTokens,
		}
	}
	return w.writeEnvelope("assistant", msg, map[string]any{
		"requestId": "req_" + strings.ReplaceAll(UUID(), "-", "")[:24],
	})
}

// writeEnvelope wraps message with the Claude-Code JSONL envelope fields and
// appends one line to the transcript.
func (w *Writer) writeEnvelope(kind string, message any, extras map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	uuid := UUID()
	var parent any
	if w.LastUUID != "" {
		parent = w.LastUUID
	}
	env := map[string]any{
		"type":        kind,
		"uuid":        uuid,
		"parentUuid":  parent,
		"sessionId":   w.SessionID,
		"cwd":         w.CWD,
		"version":     w.Version,
		"gitBranch":   w.GitBranch,
		"isSidechain": false,
		"userType":    "external",
		"entrypoint":  w.entrypoint,
		"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
		"message":     message,
	}
	for k, v := range extras {
		env[k] = v
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(w.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	w.LastUUID = uuid
	return nil
}

func gitBranch(cwd string) string {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
