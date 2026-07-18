package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Proxy is an upstream relay: it forwards remote-session requests to another
// bankai server and streams the response straight back, flushing as data
// arrives. This is the Go slice of vibelearn's upstreamproxy/relay — it lets one
// endpoint front (or fan out to) an upstream agent server. Auth is re-attached
// with the proxy's own upstream token.
type Proxy struct {
	Upstream string // base URL of the upstream server, e.g. http://host:8787
	Token    string // bearer token presented to the upstream (optional)
	HTTP     *http.Client
}

func (p *Proxy) httpClient() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	// No client timeout: SSE responses are long-lived streams.
	return &http.Client{Timeout: 0}
}

// Handler forwards POST /v1/message (and /health) to the upstream, piping the
// streamed body through unbuffered.
func (p *Proxy) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("/v1/message", p.forward)
	return mux
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, p.Upstream+"/v1/message", r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		http.Error(w, "upstream unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

// StreamMessage is a client helper: it POSTs a prompt to a bankai remote server
// and invokes onEvent for each SSE frame (event, data) until the stream ends.
// Returns the concatenated `message` text. Useful for relays and CLIs.
func StreamMessage(ctx context.Context, baseURL, token, prompt string, onEvent func(event, data string)) (string, error) {
	body := fmt.Sprintf(`{"prompt":%q}`, prompt)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/message", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("remote %d: %s", resp.StatusCode, string(b))
	}
	return parseSSEStream(resp.Body, onEvent)
}

// parseSSEStream reads an event stream, calling onEvent(event, decodedData) per
// frame, and returns the concatenation of all `message`-event data (the model
// text). Data is JSON-decoded (the server encodes each frame's data as JSON).
func parseSSEStream(r io.Reader, onEvent func(event, data string)) (string, error) {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var msg strings.Builder
	event := "message"
	for scan.Scan() {
		line := scan.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			raw := strings.TrimSpace(line[len("data:"):])
			var data string
			if json.Unmarshal([]byte(raw), &data) != nil {
				data = raw // not JSON-encoded; use as-is
			}
			if onEvent != nil {
				onEvent(event, data)
			}
			if event == "message" {
				msg.WriteString(data)
			}
		case line == "":
			event = "message" // frame boundary resets event type
		}
	}
	return msg.String(), scan.Err()
}
