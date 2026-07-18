package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// OAuth beta header Anthropic requires when using a Claude-Code OAuth token.
const BetaHeader = "oauth-2025-04-20"

// tokenURL is where refresh_token grants are exchanged.
const tokenURL = "https://console.anthropic.com/v1/oauth/token"

// clientID is the public Claude Code OAuth client id. Same value the TS bankai
// and Claude Code use — this is a PKCE public client, no secret.
const clientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// Tokens is the on-disk shape of ~/.claude/.credentials.json → claudeAiOauth.
type Tokens struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken,omitempty"`
	ExpiresAt        int64    `json:"expiresAt,omitempty"` // ms since epoch
	Scopes           []string `json:"scopes,omitempty"`
	SubscriptionType string   `json:"subscriptionType,omitempty"`
	RateLimitTier    string   `json:"rateLimitTier,omitempty"`
}

// Expired reports whether the access token is past its expiry (with 60s slack).
func (t *Tokens) Expired() bool {
	if t.ExpiresAt <= 0 {
		return false
	}
	return time.Now().UnixMilli() >= (t.ExpiresAt - 60_000)
}

// Provider loads and (auto-)refreshes Claude OAuth tokens.
// The zero value is not usable — call NewProvider.
type Provider struct {
	mu     sync.RWMutex
	tokens *Tokens
	source string // "env" | "keychain" | "file"
	path   string // populated when source == "file"
}

// NewProvider looks up tokens in order: env → macOS keychain → ~/.claude/.credentials.json.
// Returns nil, nil (no error) if nothing is found — the caller can then fall
// back to ANTHROPIC_API_KEY.
func NewProvider() (*Provider, error) {
	if t := fromEnv(); t != nil {
		return &Provider{tokens: t, source: "env"}, nil
	}
	if runtime.GOOS == "darwin" {
		if t, err := fromKeychain(); err == nil && t != nil {
			return &Provider{tokens: t, source: "keychain"}, nil
		}
	}
	if t, path, err := fromFile(); err == nil && t != nil {
		return &Provider{tokens: t, source: "file", path: path}, nil
	}
	return nil, nil
}

// AccessToken returns the current access token, refreshing it if expired.
func (p *Provider) AccessToken() (string, error) {
	p.mu.RLock()
	tok := p.tokens
	p.mu.RUnlock()
	if tok == nil {
		return "", fmt.Errorf("no OAuth tokens loaded")
	}
	if !tok.Expired() {
		return tok.AccessToken, nil
	}
	if err := p.Refresh(); err != nil {
		return "", err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.tokens.AccessToken, nil
}

// Source describes where the tokens came from ("env" / "keychain" / "file").
func (p *Provider) Source() string { return p.source }

// Refresh exchanges the refresh_token for a new access token and persists the
// result back to whichever source it came from (file only — env/keychain are
// left untouched to avoid clobbering Claude Code's own state).
func (p *Provider) Refresh() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tokens == nil || p.tokens.RefreshToken == "" {
		return fmt.Errorf("no refresh_token available")
	}
	body := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": p.tokens.RefreshToken,
		"client_id":     clientID,
	}
	if len(p.tokens.Scopes) > 0 {
		body["scope"] = strings.Join(p.tokens.Scopes, " ")
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", tokenURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("oauth refresh http %d: %s", resp.StatusCode, string(buf))
	}
	var r struct {
		AccessToken  string   `json:"access_token"`
		RefreshToken string   `json:"refresh_token"`
		ExpiresIn    int64    `json:"expires_in"`
		Scope        string   `json:"scope"`
	}
	if err := json.Unmarshal(buf, &r); err != nil {
		return fmt.Errorf("oauth refresh decode: %w", err)
	}
	p.tokens.AccessToken = r.AccessToken
	if r.RefreshToken != "" {
		p.tokens.RefreshToken = r.RefreshToken
	}
	if r.ExpiresIn > 0 {
		p.tokens.ExpiresAt = time.Now().UnixMilli() + r.ExpiresIn*1000
	}
	if r.Scope != "" {
		p.tokens.Scopes = strings.Fields(r.Scope)
	}
	if p.source == "file" && p.path != "" {
		_ = writeFile(p.path, p.tokens)
	}
	return nil
}

func fromEnv() *Tokens {
	if t := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); t != "" {
		return &Tokens{AccessToken: t}
	}
	return nil
}

// fromKeychain shells out to `security find-generic-password -w -s "Claude Code-credentials"`.
// The stored value is the same JSON blob that lives in the file.
func fromKeychain() (*Tokens, error) {
	cmd := exec.Command("security", "find-generic-password", "-w", "-s", "Claude Code-credentials")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseCredsBlob(bytes.TrimSpace(out))
}

func fromFile() (*Tokens, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	p := filepath.Join(home, ".claude", ".credentials.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	t, err := parseCredsBlob(b)
	if err != nil {
		return nil, "", err
	}
	return t, p, nil
}

// parseCredsBlob accepts either the wrapper form `{"claudeAiOauth":{...}}` or
// a bare Tokens object.
func parseCredsBlob(b []byte) (*Tokens, error) {
	var wrapper struct {
		ClaudeAiOauth *Tokens `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(b, &wrapper); err == nil && wrapper.ClaudeAiOauth != nil && wrapper.ClaudeAiOauth.AccessToken != "" {
		return wrapper.ClaudeAiOauth, nil
	}
	var bare Tokens
	if err := json.Unmarshal(b, &bare); err != nil {
		return nil, err
	}
	if bare.AccessToken == "" {
		return nil, fmt.Errorf("credentials blob has no accessToken")
	}
	return &bare, nil
}

func writeFile(p string, t *Tokens) error {
	wrapper := map[string]any{"claudeAiOauth": t}
	b, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}
