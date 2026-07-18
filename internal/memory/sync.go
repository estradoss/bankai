package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Wire is the JSON representation of a memory for team sync.
type Wire struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Body        string `json:"body"`
}

// Export returns all memories in wire form (for pushing to a team server).
func (s *Store) Export() []Wire {
	mems := s.List()
	out := make([]Wire, 0, len(mems))
	for _, m := range mems {
		out = append(out, Wire{Name: m.Name, Description: m.Description, Type: string(m.Type), Body: m.Body})
	}
	return out
}

// Import merges wire memories into the store (Save enforces the secret scanner).
// Existing names are overwritten. Returns how many were imported and any
// per-memory errors (e.g. a memory that tripped the secret scanner), keyed by
// name.
func (s *Store) Import(ws []Wire) (int, map[string]error) {
	n := 0
	errs := map[string]error{}
	for _, w := range ws {
		if err := s.Save(Memory{Name: w.Name, Description: w.Description, Type: ParseType(w.Type), Body: w.Body}); err != nil {
			errs[w.Name] = err
			continue
		}
		n++
	}
	if len(errs) == 0 {
		errs = nil
	}
	return n, errs
}

// SyncClient pushes/pulls memories to a team memory server.
type SyncClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (c *SyncClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *SyncClient) do(ctx context.Context, method string, body []byte) ([]byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/v1/memory", r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("team memory %s: %d %s", method, resp.StatusCode, string(out))
	}
	return out, nil
}

// Push uploads the store's memories to the team server.
func (c *SyncClient) Push(ctx context.Context, s *Store) (int, error) {
	ws := s.Export()
	body, err := json.Marshal(map[string][]Wire{"memories": ws})
	if err != nil {
		return 0, err
	}
	if _, err := c.do(ctx, http.MethodPost, body); err != nil {
		return 0, err
	}
	return len(ws), nil
}

// Pull downloads team memories and merges them into the store.
func (c *SyncClient) Pull(ctx context.Context, s *Store) (int, error) {
	out, err := c.do(ctx, http.MethodGet, nil)
	if err != nil {
		return 0, err
	}
	var payload struct {
		Memories []Wire `json:"memories"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return 0, err
	}
	n, _ := s.Import(payload.Memories)
	return n, nil
}
