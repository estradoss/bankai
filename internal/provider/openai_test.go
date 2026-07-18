package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/estradoss/bankai/internal/agent"
)

func TestMapToCodexModel(t *testing.T) {
	cases := map[string]string{
		"":                   DefaultCodexModel,
		"gpt-5.2-codex":      "gpt-5.2-codex",
		"claude-opus-4-7":    "gpt-5.1-codex-max",
		"claude-haiku-4-5":   "gpt-5.1-codex-mini",
		"claude-sonnet-4-6":  "gpt-5.2-codex",
		"some-unknown-model": DefaultCodexModel,
	}
	for in, want := range cases {
		if got := MapToCodexModel(in); got != want {
			t.Errorf("MapToCodexModel(%q)=%q want %q", in, got, want)
		}
	}
}

func TestToCodexInput(t *testing.T) {
	msgs := []agent.Message{
		{Role: "user", Content: []agent.ContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []agent.ContentBlock{
			{Type: "text", Text: "sure"},
			{Type: "tool_use", ID: "call_1", Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)},
		}},
		{Role: "user", Content: []agent.ContentBlock{
			{Type: "tool_result", ToolUseID: "call_1", Content: "file.txt"},
		}},
	}
	got := toCodexInput(msgs)
	b, _ := json.Marshal(got)
	s := string(b)
	for _, want := range []string{
		`"role":"user"`,
		`"type":"message"`,
		`"type":"function_call"`,
		`"call_id":"call_1"`,
		`"type":"function_call_output"`,
		`"output":"file.txt"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("codex input missing %q in %s", want, s)
		}
	}
}

func TestParseCodexSSE(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"Hel"}`,
		`data: {"type":"response.output_text.delta","delta":"lo"}`,
		`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"c1","name":"Bash"}}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"command\":"}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"\"ls\"}"}`,
		`data: {"type":"response.output_item.done","item":{"type":"function_call"}}`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}`,
		`data: [DONE]`,
	}, "\n")

	var streamed strings.Builder
	res, err := parseCodexSSE(strings.NewReader(stream), func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "Hello" {
		t.Errorf("streamed text=%q", streamed.String())
	}
	if res.StopReason != "tool_use" {
		t.Errorf("stop_reason=%q want tool_use", res.StopReason)
	}
	var gotText, gotTool bool
	for _, c := range res.Content {
		if c.Type == "text" && c.Text == "Hello" {
			gotText = true
		}
		if c.Type == "tool_use" && c.Name == "Bash" && strings.Contains(string(c.Input), `"command":"ls"`) {
			gotTool = true
		}
	}
	if !gotText || !gotTool {
		t.Errorf("content assembly wrong: %+v", res.Content)
	}
	if res.Usage.InputTokens != 10 || res.Usage.OutputTokens != 5 {
		t.Errorf("usage=%+v", res.Usage)
	}
}
