// Package lsp is a minimal Language Server Protocol client. It speaks
// Content-Length-framed JSON-RPC 2.0 over stdio to a language server, runs the
// initialize handshake, opens documents, and collects textDocument/
// publishDiagnostics notifications. It is a focused Go port of vibelearn's LSP
// subsystem (src/services/lsp/) covering diagnostics; hover/definition/rename
// and the passive-feedback loop are not yet ported.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Position is a zero-based line/character in a document.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range spans two positions.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Diagnostic is one problem reported by the server.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"` // 1=error 2=warning 3=info 4=hint
	Message  string `json:"message"`
	Source   string `json:"source"`
}

// SeverityName maps an LSP severity code to text.
func SeverityName(s int) string {
	switch s {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	}
	return "unknown"
}

// Client is a running connection to one language server.
type Client struct {
	Name string

	cmd   *exec.Cmd
	stdin io.WriteCloser
	wmu   sync.Mutex

	nextID    int
	pendingMu sync.Mutex
	pending   map[int]chan rpcResponse

	diagMu sync.Mutex
	diags  map[string][]Diagnostic // keyed by document URI
	diagCh map[string]chan struct{}

	done      chan struct{}
	closeOnce sync.Once
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	// notification fields
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Dial spawns the language server and returns a connected client.
func Dial(name, command string, args []string) (*Client, error) {
	return dialWithEnv(name, command, args, nil)
}

// dialWithEnv is Dial with an explicit environment (nil = inherit).
func dialWithEnv(name, command string, args []string, env []string) (*Client, error) {
	cmd := exec.Command(command, args...)
	if env != nil {
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command, err)
	}
	c := &Client{
		Name:    name,
		cmd:     cmd,
		stdin:   stdin,
		pending: map[int]chan rpcResponse{},
		diags:   map[string][]Diagnostic{},
		diagCh:  map[string]chan struct{}{},
		done:    make(chan struct{}),
	}
	go c.readLoop(stdout)
	return c, nil
}

// writeMessage frames a payload with a Content-Length header.
func (c *Client) writeMessage(payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err := c.stdin.Write(payload)
	return err
}

func (c *Client) readLoop(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		length, err := readHeader(br)
		if err != nil {
			break
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(br, buf); err != nil {
			break
		}
		var msg rpcResponse
		if json.Unmarshal(buf, &msg) != nil {
			continue
		}
		if msg.Method == "textDocument/publishDiagnostics" {
			c.handleDiagnostics(msg.Params)
			continue
		}
		if msg.ID != 0 {
			c.pendingMu.Lock()
			ch, ok := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.pendingMu.Unlock()
			if ok {
				ch <- msg
			}
		}
	}
	close(c.done)
}

// readHeader reads LSP headers and returns the Content-Length.
func readHeader(br *bufio.Reader) (int, error) {
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return 0, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if length < 0 {
				return 0, fmt.Errorf("missing Content-Length")
			}
			return length, nil
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			n, err := strconv.Atoi(strings.TrimSpace(line[len("content-length:"):]))
			if err != nil {
				return 0, err
			}
			length = n
		}
	}
}

func (c *Client) handleDiagnostics(params json.RawMessage) {
	var p struct {
		URI         string       `json:"uri"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	c.diagMu.Lock()
	c.diags[p.URI] = p.Diagnostics
	if ch, ok := c.diagCh[p.URI]; ok {
		close(ch)
		delete(c.diagCh, p.URI)
	}
	c.diagMu.Unlock()
}

func (c *Client) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	c.pendingMu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResponse, 1)
	c.pending[id] = ch
	c.pendingMu.Unlock()

	payload, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		return nil, err
	}
	if err := c.writeMessage(payload); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("lsp server %s exited", c.Name)
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("lsp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *Client) notify(method string, params interface{}) error {
	payload, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "method": method, "params": params,
	})
	if err != nil {
		return err
	}
	return c.writeMessage(payload)
}

// Initialize runs the LSP initialize handshake with the given workspace root.
func (c *Client) Initialize(ctx context.Context, rootURI string) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := c.call(cctx, "initialize", map[string]interface{}{
		"processId":    nil,
		"rootUri":      rootURI,
		"capabilities": map[string]interface{}{"textDocument": map[string]interface{}{"publishDiagnostics": map[string]interface{}{}}},
	})
	if err != nil {
		return err
	}
	return c.notify("initialized", map[string]interface{}{})
}

// OpenAndDiagnose opens a document and waits up to timeout for the server to
// publish diagnostics for it, returning whatever it has by then.
func (c *Client) OpenAndDiagnose(ctx context.Context, uri, languageID, text string, timeout time.Duration) ([]Diagnostic, error) {
	c.diagMu.Lock()
	wait := make(chan struct{})
	c.diagCh[uri] = wait
	c.diagMu.Unlock()

	if err := c.notify("textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri, "languageId": languageID, "version": 1, "text": text,
		},
	}); err != nil {
		return nil, err
	}

	select {
	case <-wait:
	case <-time.After(timeout):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.diagMu.Lock()
	d := c.diags[uri]
	c.diagMu.Unlock()
	return d, nil
}

// didOpen notifies the server that a document is open (idempotent enough for
// query use — servers accept repeated opens as re-syncs).
func (c *Client) didOpen(uri, languageID, text string) error {
	return c.notify("textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri, "languageId": languageID, "version": 1, "text": text,
		},
	})
}

// Hover opens the document and returns the server's hover text at the given
// 0-based line/character, or "" if there is none.
func (c *Client) Hover(ctx context.Context, uri, languageID, text string, line, char int) (string, error) {
	if err := c.didOpen(uri, languageID, text); err != nil {
		return "", err
	}
	res, err := c.call(ctx, "textDocument/hover", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": line, "character": char},
	})
	if err != nil {
		return "", err
	}
	var h struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(res, &h); err != nil || len(h.Contents) == 0 {
		return "", nil
	}
	return extractHover(h.Contents), nil
}

// extractHover flattens LSP hover contents, which may be a string, a
// {language,value} object, a {kind,value} MarkupContent, or an array of those.
func extractHover(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Value != "" {
		return obj.Value
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		parts := make([]string, 0, len(arr))
		for _, e := range arr {
			if p := extractHover(e); p != "" {
				parts = append(parts, p)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// Location is a resolved definition site.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Definition opens the document and returns definition locations at the given
// 0-based line/character.
func (c *Client) Definition(ctx context.Context, uri, languageID, text string, line, char int) ([]Location, error) {
	if err := c.didOpen(uri, languageID, text); err != nil {
		return nil, err
	}
	res, err := c.call(ctx, "textDocument/definition", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": line, "character": char},
	})
	if err != nil {
		return nil, err
	}
	// Result may be a single Location or an array of Locations.
	var one Location
	if json.Unmarshal(res, &one) == nil && one.URI != "" {
		return []Location{one}, nil
	}
	var many []Location
	_ = json.Unmarshal(res, &many)
	return many, nil
}

// TextEdit is a single replacement within a document.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// WorkspaceEdit maps document URIs to the edits to apply there. Only the
// `changes` form is handled (not the newer `documentChanges`).
type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes"`
}

// Rename asks the server to rename the symbol at the position and returns the
// resulting workspace edit (edits are not applied here).
func (c *Client) Rename(ctx context.Context, uri, languageID, text string, line, char int, newName string) (WorkspaceEdit, error) {
	if err := c.didOpen(uri, languageID, text); err != nil {
		return WorkspaceEdit{}, err
	}
	res, err := c.call(ctx, "textDocument/rename", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": line, "character": char},
		"newName":      newName,
	})
	if err != nil {
		return WorkspaceEdit{}, err
	}
	var we WorkspaceEdit
	_ = json.Unmarshal(res, &we)
	return we, nil
}

// Close shuts the server down.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		_ = c.notify("shutdown", nil)
		_ = c.stdin.Close()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}
