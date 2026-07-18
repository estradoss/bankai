package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/estradoss/bankai/internal/agent"
)

// Envelope is one JSONL line, minimally decoded.
type Envelope struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	SessionID  string          `json:"sessionId"`
	Message    json.RawMessage `json:"message"`
	Timestamp  string          `json:"timestamp"`
}

// LoadResult is what Load returns: the reconstructed message chain plus the
// last-seen uuid (so a new Writer can continue the chain).
type LoadResult struct {
	SessionID string
	Messages  []agent.Message
	LastUUID  string
}

// Load reads a Claude Code JSONL transcript and reconstructs the linear
// user/assistant message chain suitable for feeding back to the Anthropic API.
//
// Non-message events (last-prompt, mode, permission-mode, summary, ...) are
// skipped. If the chain forks (Claude Code supports branching), the newest
// leaf is followed backward via parentUuid.
func Load(path string) (*LoadResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)

	byUUID := map[string]Envelope{}
	var order []string
	var sessionID string
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}
		var env Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		if sessionID == "" {
			sessionID = env.SessionID
		}
		if env.UUID == "" || (env.Type != "user" && env.Type != "assistant") {
			continue
		}
		byUUID[env.UUID] = env
		order = append(order, env.UUID)
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}

	// Walk backward from the last user/assistant entry along parentUuid to
	// avoid picking up abandoned branches.
	if len(order) == 0 {
		return &LoadResult{SessionID: sessionID}, nil
	}
	leaf := order[len(order)-1]
	var chain []Envelope
	cur := leaf
	for cur != "" {
		env, ok := byUUID[cur]
		if !ok {
			break
		}
		chain = append(chain, env)
		cur = env.ParentUUID
	}
	// chain is leaf→root; reverse.
	sort.SliceStable(chain, func(i, j int) bool { return false })
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	res := &LoadResult{SessionID: sessionID, LastUUID: leaf}
	for _, env := range chain {
		m, err := decodeMessage(env)
		if err != nil {
			continue
		}
		res.Messages = append(res.Messages, m)
	}
	return res, nil
}

// decodeMessage extracts an agent.Message from a JSONL envelope.
func decodeMessage(env Envelope) (agent.Message, error) {
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(env.Message, &raw); err != nil {
		return agent.Message{}, err
	}
	m := agent.Message{Role: raw.Role}
	// content may be either a string (user) or an array of blocks.
	if len(raw.Content) > 0 && raw.Content[0] == '"' {
		var s string
		if err := json.Unmarshal(raw.Content, &s); err != nil {
			return m, err
		}
		m.Content = []agent.ContentBlock{agent.TextBlock(s)}
		return m, nil
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw.Content, &blocks); err != nil {
		return m, err
	}
	for _, b := range blocks {
		cb := agent.ContentBlock{}
		if t, _ := b["type"].(string); t != "" {
			cb.Type = t
		}
		switch cb.Type {
		case "text":
			cb.Text, _ = b["text"].(string)
		case "thinking":
			cb.Text, _ = b["thinking"].(string)
		case "tool_use":
			cb.ID, _ = b["id"].(string)
			cb.Name, _ = b["name"].(string)
			if in, ok := b["input"]; ok {
				j, _ := json.Marshal(in)
				cb.Input = j
			}
		case "tool_result":
			cb.ToolUseID, _ = b["tool_use_id"].(string)
			if c, ok := b["content"].(string); ok {
				cb.Content = c
			} else if arr, ok := b["content"].([]any); ok {
				// tool_result may itself contain [{type:text,text:...}]
				j, _ := json.Marshal(arr)
				cb.Content = string(j)
			}
			cb.IsError, _ = b["is_error"].(bool)
		}
		m.Content = append(m.Content, cb)
	}
	return m, nil
}

// LatestSession returns the most recently modified JSONL file under
// ~/.claude/projects/<sanitized(cwd)>/, or "" if none exists.
func LatestSession(cwd string) (string, error) {
	dir, err := ProjectDir(cwd)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var best os.FileInfo
	var bestPath string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == nil || info.ModTime().After(best.ModTime()) {
			best = info
			bestPath = filepath.Join(dir, e.Name())
		}
	}
	return bestPath, nil
}

// FindSession returns the JSONL path for a given session id, searching the
// current cwd's project dir first, then all project dirs as a fallback.
func FindSession(cwd, sessionID string) (string, error) {
	dir, err := ProjectDir(cwd)
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, sessionID+".jsonl")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	// Fallback: scan all project dirs.
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cand := filepath.Join(root, e.Name(), sessionID+".jsonl")
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("session %s not found", sessionID)
}
