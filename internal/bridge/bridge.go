// Package bridge implements a minimal IDE integration bridge, the Go analogue
// of vibelearn's src/bridge/. An IDE extension discovers the running agent via a
// lockfile under ~/.claude/ide/<port>.lock and talks to a small HTTP server:
// the IDE pushes editor state (current selection, diagnostics) and polls for
// agent→IDE commands (open a file, show a diff). The agent reads/writes this
// state through the ide_* tools. Transport is plain HTTP/JSON so it needs no
// external dependency and is fully testable.
package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// Selection is the IDE's current editor selection.
type Selection struct {
	File      string `json:"file"`
	Text      string `json:"text"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

// Command is an agent→IDE request (e.g. open a file or show a diff) the IDE
// picks up by polling GET /v1/commands.
type Command struct {
	Kind string `json:"kind"` // "openFile" | "showDiff"
	File string `json:"file"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
}

// Bridge holds shared IDE state between the editor and the agent.
type Bridge struct {
	mu        sync.Mutex
	selection Selection
	diags     map[string]string // file -> rendered diagnostics
	pending   []Command         // agent→IDE queue
}

func New() *Bridge { return &Bridge{diags: map[string]string{}} }

// Selection returns the current editor selection.
func (b *Bridge) Selection() Selection {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.selection
}

// Enqueue adds an agent→IDE command for the editor to pick up.
func (b *Bridge) Enqueue(c Command) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = append(b.pending, c)
}

// drain returns and clears the pending agent→IDE commands.
func (b *Bridge) drain() []Command {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.pending
	b.pending = nil
	return out
}

// Handler exposes the bridge over HTTP.
func (b *Bridge) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("/v1/selection", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var s Selection
			if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
				http.Error(w, "bad selection", http.StatusBadRequest)
				return
			}
			b.mu.Lock()
			b.selection = s
			b.mu.Unlock()
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(b.Selection())
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/v1/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		var d struct {
			File string `json:"file"`
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			http.Error(w, "bad diagnostics", http.StatusBadRequest)
			return
		}
		b.mu.Lock()
		b.diags[d.File] = d.Text
		b.mu.Unlock()
	})
	mux.HandleFunc("/v1/commands", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string][]Command{"commands": b.drain()})
	})
	return mux
}

// WriteLockfile records the bridge's discovery info at
// ~/.claude/ide/<port>.lock so an IDE extension can find and connect to it.
// Returns the lockfile path.
func WriteLockfile(homeDir string, port int, authToken string, workspaceFolders []string) (string, error) {
	dir := filepath.Join(homeDir, ".claude", "ide")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.lock", port))
	info := map[string]any{
		"pid":              os.Getpid(),
		"transport":        "http",
		"port":             port,
		"authToken":        authToken,
		"ideName":          "bankai",
		"workspaceFolders": workspaceFolders,
	}
	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// RemoveLockfile deletes the lockfile for a port (best-effort).
func RemoveLockfile(homeDir string, port int) {
	_ = os.Remove(filepath.Join(homeDir, ".claude", "ide", fmt.Sprintf("%d.lock", port)))
}
