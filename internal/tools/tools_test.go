package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "sub/main.go", true}, // basename match when no slash
		{"**/*.go", "a/b/c.go", true},
		{"**/*.go", "c.go", true},
		{"src/**/*.ts", "src/a/b.ts", true},
		{"src/**/*.ts", "lib/a/b.ts", false},
		{"internal/*.go", "internal/x.go", true},
		{"internal/*.go", "internal/x/y.go", false},
		{"**/foo", "a/b/foo", true},
		{"**", "any/thing/here", true},
	}
	for _, c := range cases {
		if got := matchGlob(c.pat, c.name); got != c.want {
			t.Errorf("matchGlob(%q,%q)=%v want %v", c.pat, c.name, got, c.want)
		}
	}
}

func TestGrepGoFallback(t *testing.T) {
	dir := t.TempDir()
	must := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("a.txt", "hello world\nfoo bar\n")
	must("b.txt", "nothing here\n")

	in, _ := json.Marshal(grepInput{Pattern: "foo", Path: dir, OutputMode: "files_with_matches"})
	res, err := grepGo(mustUnmarshal(t, in))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "a.txt") || strings.Contains(res.Output, "b.txt") {
		t.Errorf("unexpected files_with_matches output: %q", res.Output)
	}

	in2, _ := json.Marshal(grepInput{Pattern: "foo", Path: dir, OutputMode: "content", LineNums: true})
	res2, _ := grepGo(mustUnmarshal(t, in2))
	if !strings.Contains(res2.Output, ":2:foo bar") {
		t.Errorf("content mode missing line number: %q", res2.Output)
	}
}

func mustUnmarshal(t *testing.T, b []byte) grepInput {
	var g grepInput
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestHTMLToText(t *testing.T) {
	html := `<html><head><style>x{}</style><script>bad()</script></head><body><h1>Title</h1><p>Hello &amp; world</p></body></html>`
	got := htmlToText(html)
	if strings.Contains(got, "bad()") || strings.Contains(got, "<") {
		t.Errorf("html not stripped: %q", got)
	}
	if !strings.Contains(got, "Title") || !strings.Contains(got, "Hello & world") {
		t.Errorf("text/entities lost: %q", got)
	}
}

func TestTodoStore(t *testing.T) {
	s := NewTodoStore()
	tw := TodoWriteTool{Store: s}
	in, _ := json.Marshal(map[string]any{"todos": []TodoItem{
		{Content: "step one", Status: "completed"},
		{Content: "step two", Status: "in_progress"},
	}})
	if _, err := tw.Call(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	out := s.Render()
	if !strings.Contains(out, "[x] step one") || !strings.Contains(out, "[~] step two") {
		t.Errorf("render mismatch: %q", out)
	}
}
