// Package codex implements the OpenAI Codex OAuth 2.0 PKCE flow and token
// storage, mirroring the TypeScript src/services/oauth/codex-client.ts. It is
// completely separate from the Anthropic OAuth flow: it talks to OpenAI's auth
// server (auth.openai.com) and yields tokens used against ChatGPT's Codex
// backend (chatgpt.com/backend-api/codex/responses).
package codex

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// OAuth constants — extracted from the Codex CLI registration.
const (
	ClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	AuthorizeURL = "https://auth.openai.com/oauth/authorize"
	TokenURL     = "https://auth.openai.com/oauth/token"
	RedirectURI  = "http://localhost:1455/auth/callback"
	Scopes       = "openid profile email offline_access"
	jwtAuthClaim = "https://api.openai.com/auth"
)

// Tokens is the persisted Codex credential set.
type Tokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"` // epoch ms
	AccountID    string `json:"accountId"`
}

// ---- PKCE helpers ----

func randB64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ---- JWT account id ----

func extractAccountID(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// tolerate standard padding
		raw, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return "", fmt.Errorf("decode JWT payload: %w", err)
		}
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	claim, ok := payload[jwtAuthClaim]
	if !ok {
		return "", fmt.Errorf("no auth claim in token")
	}
	var authObj struct {
		AccountID string `json:"chatgpt_account_id"`
	}
	if err := json.Unmarshal(claim, &authObj); err != nil {
		return "", err
	}
	if authObj.AccountID == "" {
		return "", fmt.Errorf("no account id in token")
	}
	return authObj.AccountID, nil
}

// ---- token endpoint ----

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func postToken(form url.Values) (*Tokens, error) {
	resp, err := http.PostForm(TokenURL, form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var tr tokenResp
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 || tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, fmt.Errorf("codex token endpoint http %d", resp.StatusCode)
	}
	acct, err := extractAccountID(tr.AccessToken)
	if err != nil {
		return nil, err
	}
	return &Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second).UnixMilli(),
		AccountID:    acct,
	}, nil
}

func exchangeCode(code, verifier string) (*Tokens, error) {
	return postToken(url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {RedirectURI},
	})
}

func refresh(refreshToken string) (*Tokens, error) {
	return postToken(url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ClientID},
	})
}

// ---- login flow ----

func buildAuthURL() (authURL, verifier, state string) {
	verifier = randB64(48)
	state = randB64(24)
	u, _ := url.Parse(AuthorizeURL)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", ClientID)
	q.Set("redirect_uri", RedirectURI)
	q.Set("scope", Scopes)
	q.Set("code_challenge", codeChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", "bankai")
	u.RawQuery = q.Encode()
	return u.String(), verifier, state
}

func openBrowser(u string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	_ = exec.Command(cmd, append(args, u)...).Start()
}

// Login runs the full PKCE flow: opens the browser, listens on port 1455 for
// the callback, exchanges the code, and persists the tokens. Blocks until the
// user completes the flow or timeout elapses.
func Login() (*Tokens, error) {
	authURL, verifier, state := buildAuthURL()

	ln, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return nil, fmt.Errorf("port 1455 unavailable (needed for Codex OAuth): %w", err)
	}

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- fmt.Errorf("oauth state mismatch")
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("no authorization code")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h2>OpenAI authentication completed.</h2><p>You can close this window and return to your terminal.</p></body></html>"))
		codeCh <- code
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	fmt.Fprintf(os.Stderr, "Opening browser for Codex login. If it doesn't open, visit:\n%s\n", authURL)
	openBrowser(authURL)

	select {
	case code := <-codeCh:
		toks, err := exchangeCode(code, verifier)
		if err != nil {
			return nil, err
		}
		if err := Save(toks); err != nil {
			return nil, err
		}
		return toks, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("codex login timed out")
	}
}

// ---- token storage ----

func storePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".bankai", "codex-oauth.json"), nil
}

func Save(t *Tokens) error {
	p, err := storePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func load() (*Tokens, error) {
	p, err := storePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var t Tokens
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Logout removes stored Codex credentials.
func Logout() error {
	p, err := storePath()
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ---- provider (auto-refreshing token source) ----

// Provider yields a valid Codex access token, refreshing when near expiry, and
// exposes the ChatGPT account id required by the Codex backend.
type Provider struct {
	mu   sync.Mutex
	toks *Tokens
}

// NewProvider loads stored Codex tokens. Returns nil if none exist.
func NewProvider() *Provider {
	t, err := load()
	if err != nil || t == nil || t.AccessToken == "" {
		return nil
	}
	return &Provider{toks: t}
}

func (p *Provider) AccountID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.toks.AccountID
}

// AccessToken returns a valid access token, refreshing if it expires within 60s.
func (p *Provider) AccessToken() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	slack := int64(60_000)
	if time.Now().UnixMilli()+slack < p.toks.ExpiresAt {
		return p.toks.AccessToken, nil
	}
	refreshed, err := refresh(p.toks.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("codex token refresh failed (re-run `bankai codex login`): %w", err)
	}
	p.toks = refreshed
	_ = Save(refreshed)
	return refreshed.AccessToken, nil
}
