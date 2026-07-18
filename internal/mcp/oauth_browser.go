package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

// parseLoopback validates that a redirect URI is an http loopback URL.
func parseLoopback(redirectURI string) (*url.URL, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("redirect URI must be http loopback, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil, fmt.Errorf("redirect URI host must be loopback, got %q", host)
	}
	return u, nil
}

// BrowserAuthorizer opens the authorization URL in the user's browser and runs
// a transient local HTTP server on the redirect URI's port to catch the
// redirect and extract the authorization code. It is the default Authorizer for
// interactive use. redirectURI must be a loopback http URL (e.g.
// http://127.0.0.1:8765/callback).
func BrowserAuthorizer(ctx context.Context, authURL, redirectURI string) (string, error) {
	host, path, err := loopbackHostPath(redirectURI)
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", host)
	if err != nil {
		return "", fmt.Errorf("could not bind callback listener on %s: %w", host, err)
	}
	defer ln.Close()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			fmt.Fprintf(w, "Authorization failed: %s. You can close this tab.", e)
			errCh <- fmt.Errorf("authorization error: %s", e)
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "Authorization complete. You can close this tab and return to bankai.")
		codeCh <- code
	})}
	go srv.Serve(ln)
	defer srv.Close()

	if err := openBrowser(authURL); err != nil {
		// Non-fatal: the user can open the URL manually.
		fmt.Printf("Open this URL to authorize:\n%s\n", authURL)
	}

	select {
	case code := <-codeCh:
		return code, nil
	case err := <-errCh:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("timed out waiting for authorization")
	}
}

// loopbackHostPath splits a loopback redirect URI into a listen address and path.
func loopbackHostPath(redirectURI string) (hostPort, path string, err error) {
	u, err := parseLoopback(redirectURI)
	if err != nil {
		return "", "", err
	}
	p := u.Path
	if p == "" {
		p = "/"
	}
	return u.Host, p, nil
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
