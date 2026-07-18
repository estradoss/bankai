package mcp

// OAuth 2.1 authorization-code flow for MCP HTTP servers, per the MCP auth spec:
// protected-resource metadata (RFC 9728) → authorization-server metadata
// (RFC 8414) → dynamic client registration (RFC 7591) → PKCE authorization-code
// exchange (RFC 7636). The interactive "open a browser, catch the redirect"
// step is injected (Authorizer) so the flow is testable and so headless callers
// can supply their own consent mechanism.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Token is an issued OAuth token.
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

// Header returns the Authorization header value for this token.
func (t Token) Header() string {
	typ := t.TokenType
	if typ == "" {
		typ = "Bearer"
	}
	return typ + " " + t.AccessToken
}

// authServerMeta is the subset of RFC 8414 metadata we use.
type authServerMeta struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
}

// resourceMeta is the subset of RFC 9728 protected-resource metadata we use.
type resourceMeta struct {
	AuthorizationServers []string `json:"authorization_servers"`
}

// Authorizer drives the user-consent step: given the authorization URL and the
// redirect URI the flow expects, it returns the authorization code the server
// redirected back with. The default (BrowserAuthorizer) opens a browser and
// runs a local callback server; tests inject a stub.
type Authorizer func(ctx context.Context, authURL, redirectURI string) (code string, err error)

// OAuthConfig parameterizes the flow.
type OAuthConfig struct {
	ResourceURL string     // the MCP server URL (protected resource)
	HTTP        *http.Client
	Authorize   Authorizer // required
	RedirectURI string     // e.g. http://127.0.0.1:8765/callback
	ClientName  string
}

func pkcePair() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func (c *OAuthConfig) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *OAuthConfig) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("GET %s: %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// discover finds the authorization server and its metadata for the resource.
func (c *OAuthConfig) discover(ctx context.Context) (authServerMeta, error) {
	base, err := url.Parse(c.ResourceURL)
	if err != nil {
		return authServerMeta{}, err
	}
	origin := base.Scheme + "://" + base.Host

	// Protected-resource metadata → authorization server(s).
	authServer := origin
	var rm resourceMeta
	if err := c.getJSON(ctx, origin+"/.well-known/oauth-protected-resource", &rm); err == nil && len(rm.AuthorizationServers) > 0 {
		authServer = strings.TrimRight(rm.AuthorizationServers[0], "/")
	}

	// Authorization-server metadata (try oauth then oidc well-known).
	var meta authServerMeta
	for _, wk := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		if err := c.getJSON(ctx, authServer+wk, &meta); err == nil && meta.AuthorizationEndpoint != "" {
			return meta, nil
		}
	}
	return authServerMeta{}, fmt.Errorf("no authorization-server metadata at %s", authServer)
}

// register performs dynamic client registration, returning a client_id.
func (c *OAuthConfig) register(ctx context.Context, meta authServerMeta) (string, error) {
	if meta.RegistrationEndpoint == "" {
		return "", fmt.Errorf("server does not support dynamic client registration")
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                c.ClientName,
		"redirect_uris":              []string{c.RedirectURI},
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RegistrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("registration failed: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ClientID == "" {
		return "", fmt.Errorf("registration returned no client_id")
	}
	return out.ClientID, nil
}

// exchange swaps an authorization code for a token using PKCE.
func (c *OAuthConfig) exchange(ctx context.Context, meta authServerMeta, clientID, code, verifier string) (Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.RedirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return Token{}, fmt.Errorf("token exchange failed: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var tok Token
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return Token{}, err
	}
	if tok.AccessToken == "" {
		return Token{}, fmt.Errorf("token response had no access_token")
	}
	return tok, nil
}

// Authenticate runs the full flow and returns an access token.
func (c *OAuthConfig) Authenticate(ctx context.Context) (Token, error) {
	if c.Authorize == nil {
		return Token{}, fmt.Errorf("no Authorizer configured")
	}
	if c.ClientName == "" {
		c.ClientName = "bankai"
	}
	meta, err := c.discover(ctx)
	if err != nil {
		return Token{}, err
	}
	clientID, err := c.register(ctx, meta)
	if err != nil {
		return Token{}, err
	}
	verifier, challenge, err := pkcePair()
	if err != nil {
		return Token{}, err
	}
	state, err := randState()
	if err != nil {
		return Token{}, err
	}
	authURL := meta.AuthorizationEndpoint + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {c.RedirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}.Encode()

	code, err := c.Authorize(ctx, authURL, c.RedirectURI)
	if err != nil {
		return Token{}, err
	}
	return c.exchange(ctx, meta, clientID, code, verifier)
}

func randState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
