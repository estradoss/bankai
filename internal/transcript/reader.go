package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	IsSidechain bool           `json:"isSidechain"`
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

	// Keep EVERY uuid-bearing record, not just user/assistant. Real Claude Code
	// transcripts interleave other record types (system, file-history-snapshot,
	// attachment, …) into the parentUuid chain; the walk must bridge across them
	// or it stops at the first non-message parent. (Matches learnvibe's
	// loadTranscriptFile, which maps all entries before buildConversationChain.)
	byUUID := map[string]Envelope{}
	var order []string // uuids of user/assistant entries, in file order
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
		if env.UUID == "" {
			continue
		}
		byUUID[env.UUID] = env
		if !env.IsSidechain && (env.Type == "user" || env.Type == "assistant") {
			order = append(order, env.UUID)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}

	// Leaf = the last main-conversation message; walk backward along parentUuid,
	// bridging over any non-message records, to skip abandoned branches.
	if len(order) == 0 {
		return &LoadResult{SessionID: sessionID}, nil
	}
	leaf := order[len(order)-1]
	var chain []Envelope
	seen := map[string]bool{}
	for cur := leaf; cur != "" && !seen[cur]; {
		seen[cur] = true
		env, ok := byUUID[cur]
		if !ok {
			break
		}
		chain = append(chain, env)
		cur = env.ParentUUID
	}
	// chain is leaf→root; reverse to root→leaf.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	res := &LoadResult{SessionID: sessionID, LastUUID: leaf}
	var msgs []agent.Message
	for _, env := range chain {
		if env.IsSidechain || (env.Type != "user" && env.Type != "assistant") {
			continue // bridged-over record — keep the link, drop the content
		}
		m, err := decodeMessage(env)
		if err != nil {
			continue
		}
		msgs = append(msgs, m)
	}
	res.Messages = sanitizeChain(msgs)
	return res, nil
}

// sanitizeChain makes a raw envelope-per-line message list safe to replay to
// the Anthropic API: it drops tool_use blocks with no matching tool_result (and
// vice versa — interrupted turns), removes emptied messages, then coalesces
// consecutive same-role messages so every assistant turn's tool_use blocks are
// immediately followed by one user message carrying their tool_result blocks.
func sanitizeChain(msgs []agent.Message) []agent.Message {
	// Pair tool_use ids with tool_result ids across the whole chain.
	useIDs := map[string]bool{}
	resultIDs := map[string]bool{}
	for _, m := range msgs {
		for _, c := range m.Content {
			switch c.Type {
			case "tool_use":
				useIDs[c.ID] = true
			case "tool_result":
				resultIDs[c.ToolUseID] = true
			}
		}
	}
	var kept []agent.Message
	for _, m := range msgs {
		var content []agent.ContentBlock
		for _, c := range m.Content {
			switch c.Type {
			case "tool_use":
				if !resultIDs[c.ID] {
					continue // dangling call (interrupted) — drop it
				}
			case "tool_result":
				if !useIDs[c.ToolUseID] {
					continue // orphan result — drop it
				}
			}
			content = append(content, c)
		}
		if len(content) == 0 {
			continue
		}
		m.Content = content
		// Coalesce with the previous message if same role.
		if n := len(kept); n > 0 && kept[n-1].Role == m.Role {
			kept[n-1].Content = append(kept[n-1].Content, content...)
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

// FirstPrompt returns the first real user text in a transcript (for labeling a
// session in the /resume picker), or "" if none. Skips tool_result-only and
// slash/hidden-prompt user turns.
func FirstPrompt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}
		var env Envelope
		if err := json.Unmarshal(line, &env); err != nil || env.Type != "user" || env.IsSidechain {
			continue
		}
		m, err := decodeMessage(env)
		if err != nil {
			continue
		}
		for _, c := range m.Content {
			if c.Type != "text" {
				continue
			}
			t := strings.TrimSpace(c.Text)
			// Skip hidden/system-injected prompts and command echoes.
			if t == "" || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "[") {
				continue
			}
			t = strings.ReplaceAll(t, "\n", " ")
			return t
		}
	}
	return ""
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
