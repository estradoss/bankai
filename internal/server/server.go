// Package server exposes a bankai engine as a remote session over HTTP with
// Server-Sent-Events streaming. It is a focused, stdlib-only slice of
// vibelearn's remote/server subsystem: a single agent session reachable over
// the network, bearer-token authed, with streamed model output. (WebSocket
// sessions, the multi-agent coordinator, permission bridging, and team memory
// sync remain part of the broader Remote item.)
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/estradoss/bankai/internal/engine"
	"github.com/estradoss/bankai/internal/permission"
)

// Engine is the subset of *engine.Engine the server needs (eases testing).
type Engine interface {
	Submit(ctx context.Context, input string) error
	LastAssistantText() string
}

var _ Engine = (*engine.Engine)(nil)

// Server serves one engine session. Requests are serialized (one turn at a
// time) since the engine holds mutable conversation state.
type Server struct {
	eng   Engine
	token string
	mu    sync.Mutex

	// Permission bridge: pending remote approval requests keyed by id, resolved
	// by POST /v1/permission.
	pmu     sync.Mutex
	permSeq int
	pending map[string]chan permission.Decision
}

// New builds a server for eng. If token is non-empty, requests must present it
// as "Authorization: Bearer <token>".
func New(eng Engine, token string) *Server {
	return &Server{eng: eng, token: token, pending: map[string]chan permission.Decision{}}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/v1/message", s.handleMessage)
	mux.HandleFunc("/v1/permission", s.handlePermission)
	return mux
}

// handlePermission resolves a pending permission request. Body:
// {"id":"...","decision":"allow_once|allow_always|deny"}.
func (s *Server) handlePermission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in struct {
		ID       string `json:"id"`
		Decision string `json:"decision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ID == "" {
		http.Error(w, "expected {\"id\":..,\"decision\":..}", http.StatusBadRequest)
		return
	}
	s.pmu.Lock()
	ch, ok := s.pending[in.ID]
	if ok {
		delete(s.pending, in.ID)
	}
	s.pmu.Unlock()
	if !ok {
		http.Error(w, "no such pending permission", http.StatusNotFound)
		return
	}
	ch <- decodeDecision(in.Decision)
	w.WriteHeader(http.StatusNoContent)
}

func decodeDecision(s string) permission.Decision {
	switch s {
	case "allow_once", "allow", "yes", "y":
		return permission.DecideAllowOnce
	case "allow_always", "always", "a":
		return permission.DecideAllowAlways
	default:
		return permission.DecideDeny
	}
}

// remoteAsker returns a permission.Asker that emits a `permission` SSE frame to
// the client and blocks until POST /v1/permission resolves it (or ctx/timeout).
func (s *Server) remoteAsker(ctx context.Context, w http.ResponseWriter, flush func()) permission.Asker {
	return func(req permission.Request) permission.Decision {
		s.pmu.Lock()
		s.permSeq++
		id := "p" + strconv.Itoa(s.permSeq)
		ch := make(chan permission.Decision, 1)
		s.pending[id] = ch
		s.pmu.Unlock()

		payload, _ := json.Marshal(map[string]string{"id": id, "tool": req.Tool, "input": string(req.Input)})
		fmt.Fprintf(w, "event: permission\ndata: %s\n\n", payload)
		flush()

		select {
		case d := <-ch:
			return d
		case <-ctx.Done():
			return permission.DecideDeny
		}
	}
}

func (s *Server) authed(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+s.token
}

// handleMessage accepts {"prompt": "..."} and streams the model's text output
// as SSE `data:` events, ending with an `event: done` frame.
func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Prompt == "" {
		http.Error(w, "expected JSON body {\"prompt\": \"...\"}", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Serialize turns: the engine has mutable state.
	s.mu.Lock()
	defer s.mu.Unlock()

	e, isRealEngine := s.eng.(*engine.Engine)
	var prevOnText func(string)
	if isRealEngine {
		prevOnText = e.OnText
		e.OnText = func(chunk string) {
			writeSSE(w, "message", chunk)
			flusher.Flush()
		}
		defer func() { e.OnText = prevOnText }()
		// Permission bridge: route approval prompts to this client over SSE.
		if e.Perms != nil {
			prevAsker := e.Perms.Asker
			e.Perms.Asker = s.remoteAsker(r.Context(), w, flusher.Flush)
			defer func() { e.Perms.Asker = prevAsker }()
		}
	}

	if err := s.eng.Submit(r.Context(), in.Prompt); err != nil {
		writeSSE(w, "error", err.Error())
		flusher.Flush()
		return
	}
	// For engines without streaming callbacks, emit the final text once.
	if !isRealEngine {
		writeSSE(w, "message", s.eng.LastAssistantText())
	}
	writeSSE(w, "done", "")
	flusher.Flush()
}

// writeSSE writes one Server-Sent-Events frame. Multi-line data is split into
// multiple `data:` lines per the SSE spec.
func writeSSE(w http.ResponseWriter, event, data string) {
	fmt.Fprintf(w, "event: %s\n", event)
	// Encode as JSON to keep newlines/framing intact on the client side.
	payload, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", payload)
}

// ListenAndServe starts the server on addr (e.g. ":8787").
func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}
