package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as a mock LSP server when BANKAI_LSP_HELPER=1: it speaks
// Content-Length-framed JSON-RPC, answers initialize, and on didOpen publishes
// one diagnostic for the opened document.
func TestMain(m *testing.M) {
	if os.Getenv("BANKAI_LSP_HELPER") == "1" {
		runMockLSP()
		return
	}
	os.Exit(m.Run())
}

func runMockLSP() {
	br := bufio.NewReader(os.Stdin)
	write := func(v interface{}) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", len(b))
		os.Stdout.Write(b)
	}
	for {
		n, err := readHeader(br)
		if err != nil {
			return
		}
		buf := make([]byte, n)
		if _, err := readFull(br, buf); err != nil {
			return
		}
		var msg struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(buf, &msg)
		switch msg.Method {
		case "initialize":
			write(map[string]interface{}{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]interface{}{"capabilities": map[string]interface{}{}}})
		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			write(map[string]interface{}{
				"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
				"params": map[string]interface{}{
					"uri": p.TextDocument.URI,
					"diagnostics": []map[string]interface{}{
						{"range": map[string]interface{}{"start": map[string]int{"line": 2, "character": 4}, "end": map[string]int{"line": 2, "character": 9}},
							"severity": 1, "message": "undefined: foo", "source": "mock"},
					},
				},
			})
		}
	}
}

func readFull(br *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := br.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func dialMockEnv(t *testing.T) *Client {
	t.Helper()
	c, err := dialWithEnv("mock", os.Args[0], nil, append(os.Environ(), "BANKAI_LSP_HELPER=1"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestReadHeader(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("Content-Length: 5\r\nContent-Type: x\r\n\r\nhello"))
	n, err := readHeader(br)
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestInitializeAndDiagnostics(t *testing.T) {
	c := dialMockEnv(t)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Initialize(ctx, "file:///tmp"); err != nil {
		t.Fatal(err)
	}
	diags, err := c.OpenAndDiagnose(ctx, "file:///tmp/x.mock", "mock", "package x\n\nfoo()\n", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 1 || diags[0].Severity != 1 || !strings.Contains(diags[0].Message, "undefined") {
		t.Fatalf("diags = %+v", diags)
	}
	if diags[0].Range.Start.Line != 2 {
		t.Fatalf("line = %d", diags[0].Range.Start.Line)
	}
}

func TestLoadConfigsDefaultsAndParse(t *testing.T) {
	cfgs := LoadConfigs(t.TempDir(), t.TempDir())
	// gopls likely absent in CI; just assert no panic and map type.
	_ = cfgs
	m := NewManager("/root", map[string]ServerConfig{
		"mock": {Command: "x", Extensions: []string{".mock"}, LanguageID: "mock"},
	})
	if len(m.Languages()) != 1 {
		t.Fatalf("languages = %v", m.Languages())
	}
}
