package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
)

// EngineFactory builds a fresh engine for a new remote session.
type EngineFactory func() Engine

// Manager is a RemoteSessionManager: it holds many independent agent sessions
// keyed by id, each with its own engine, and routes messages to them. A slice
// of vibelearn's remote coordinator. Concurrency-safe; per-session turns are
// serialized by the underlying Server.
type Manager struct {
	factory EngineFactory
	token   string
	mu      sync.Mutex
	nextID  int
	servers map[string]*Server
}

// NewManager builds a multi-session manager. token (if non-empty) is required on
// every request as a bearer token.
func NewManager(factory EngineFactory, token string) *Manager {
	return &Manager{factory: factory, token: token, servers: map[string]*Server{}}
}

func (m *Manager) authed(r *http.Request) bool {
	if m.token == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+m.token
}

// create makes a new session and returns its id.
func (m *Manager) create() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("s%d", m.nextID)
	m.servers[id] = New(m.factory(), m.token)
	return id
}

func (m *Manager) get(id string) (*Server, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servers[id]
	return s, ok
}

func (m *Manager) list() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Handler routes:
//
//	GET  /health
//	POST /v1/sessions                     create a session → {"id": "..."}
//	GET  /v1/sessions                     list session ids
//	POST /v1/sessions/{id}/message        stream a turn (SSE), see Server
func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !m.authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodPost:
			id := m.create()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string][]string{"sessions": m.list()})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/v1/sessions/", func(w http.ResponseWriter, r *http.Request) {
		id := sessionIDFromPath(r.URL.Path)
		if id == "" {
			http.Error(w, "bad path; use /v1/sessions/{id}/message", http.StatusBadRequest)
			return
		}
		s, ok := m.get(id)
		if !ok {
			http.Error(w, "no such session", http.StatusNotFound)
			return
		}
		s.handleMessage(w, r)
	})
	return mux
}

// sessionIDFromPath extracts "{id}" from "/v1/sessions/{id}/message".
func sessionIDFromPath(path string) string {
	const prefix = "/v1/sessions/"
	if len(path) <= len(prefix) {
		return ""
	}
	rest := path[len(prefix):]
	i := 0
	for i < len(rest) && rest[i] != '/' {
		i++
	}
	if i == 0 || rest[i:] != "/message" {
		return ""
	}
	return rest[:i]
}

// ListenAndServe starts the multi-session manager on addr.
func (m *Manager) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, m.Handler())
}
