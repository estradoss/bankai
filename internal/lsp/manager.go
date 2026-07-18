package lsp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ServerConfig is one entry under settings.json "lspServers", keyed by a
// language id. Extensions lists the file suffixes this server handles.
type ServerConfig struct {
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	Extensions []string `json:"extensions"`
	LanguageID string   `json:"languageId"`
}

type settingsFile struct {
	LSPServers map[string]ServerConfig `json:"lspServers"`
}

// LoadConfigs reads lspServers from user + project settings.json, then adds
// built-in defaults for languages whose server is on PATH (currently: gopls for
// Go). Explicit config wins over defaults.
func LoadConfigs(homeDir, projectDir string) map[string]ServerConfig {
	out := map[string]ServerConfig{}
	read := func(p string) {
		raw, err := os.ReadFile(p)
		if err != nil {
			return
		}
		var sf settingsFile
		if json.Unmarshal(raw, &sf) != nil {
			return
		}
		for lang, cfg := range sf.LSPServers {
			if cfg.LanguageID == "" {
				cfg.LanguageID = lang
			}
			out[lang] = cfg
		}
	}
	if homeDir != "" {
		read(filepath.Join(homeDir, ".claude", "settings.json"))
	}
	if projectDir != "" {
		read(filepath.Join(projectDir, ".claude", "settings.json"))
	}
	if _, ok := out["go"]; !ok {
		if _, err := exec.LookPath("gopls"); err == nil {
			out["go"] = ServerConfig{Command: "gopls", Extensions: []string{".go"}, LanguageID: "go"}
		}
	}
	return out
}

// Manager lazily starts language servers and routes diagnostics requests to the
// right one by file extension.
type Manager struct {
	root    string
	configs map[string]ServerConfig
	byExt   map[string]string // ext -> language key

	mu      sync.Mutex
	clients map[string]*Client // language key -> client
}

// NewManager builds a manager for the given workspace root and configs.
func NewManager(root string, configs map[string]ServerConfig) *Manager {
	byExt := map[string]string{}
	for lang, cfg := range configs {
		for _, e := range cfg.Extensions {
			byExt[strings.ToLower(e)] = lang
		}
	}
	return &Manager{root: root, configs: configs, byExt: byExt, clients: map[string]*Client{}}
}

// Languages returns the configured language keys.
func (m *Manager) Languages() []string {
	out := make([]string, 0, len(m.configs))
	for l := range m.configs {
		out = append(out, l)
	}
	return out
}

// clientFor returns (starting if needed) the client handling file's extension.
func (m *Manager) clientFor(ctx context.Context, file string) (*Client, string, error) {
	lang, ok := m.byExt[strings.ToLower(filepath.Ext(file))]
	if !ok {
		return nil, "", nil // no server for this type
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[lang]; ok {
		return c, m.configs[lang].LanguageID, nil
	}
	cfg := m.configs[lang]
	c, err := Dial(lang, cfg.Command, cfg.Args)
	if err != nil {
		return nil, "", err
	}
	rootURI := "file://" + m.root
	if err := c.Initialize(ctx, rootURI); err != nil {
		c.Close()
		return nil, "", err
	}
	m.clients[lang] = c
	return c, cfg.LanguageID, nil
}

// Diagnose returns diagnostics for a file, starting its server on first use.
// A nil slice with nil error means no server is configured for the file type.
func (m *Manager) Diagnose(ctx context.Context, file string) ([]Diagnostic, error) {
	c, langID, err := m.clientFor(ctx, file)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	text, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	return c.OpenAndDiagnose(ctx, "file://"+abs, langID, string(text), 3*time.Second)
}

// Close shuts every started server down.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clients {
		c.Close()
	}
}
