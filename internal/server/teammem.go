package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"

	"github.com/estradoss/bankai/internal/memory"
)

// TeamMemory is a shared memory store for team sync: clients POST their
// memories (merged by name) and GET the merged set. A slice of vibelearn's
// team memory sync — the server side. Concurrency-safe, in-process.
type TeamMemory struct {
	token string
	mu    sync.Mutex
	byName map[string]memory.Wire
}

// NewTeamMemory builds a team memory endpoint. token (if set) is required.
func NewTeamMemory(token string) *TeamMemory {
	return &TeamMemory{token: token, byName: map[string]memory.Wire{}}
}

func (t *TeamMemory) authed(r *http.Request) bool {
	if t.token == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+t.token
}

// Register wires the /v1/memory routes onto a mux.
func (t *TeamMemory) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/memory", func(w http.ResponseWriter, r *http.Request) {
		if !t.authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodPost:
			var payload struct {
				Memories []memory.Wire `json:"memories"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			t.mu.Lock()
			for _, m := range payload.Memories {
				if m.Name != "" {
					t.byName[m.Name] = m
				}
			}
			t.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string][]memory.Wire{"memories": t.snapshot()})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (t *TeamMemory) snapshot() []memory.Wire {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]memory.Wire, 0, len(t.byName))
	for _, m := range t.byName {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
