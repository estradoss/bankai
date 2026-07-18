package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolSearchTool searches the tool registry by keyword (or exact name) and
// returns matching tools' full JSONSchema definitions in a <functions> block —
// the same encoding as the main tool list. Ported from vibelearn's
// ToolSearchTool. In bankai all tools are already exposed, so this is a
// discovery aid rather than a deferral mechanism.
type ToolSearchTool struct{ Reg *Registry }

func (ToolSearchTool) Name() string { return "ToolSearch" }

func (ToolSearchTool) Description() string {
	return "Search available tools and return their full schema definitions.\n\n" +
		"Query forms:\n" +
		"- \"select:Read,Edit,Grep\" — fetch these exact tools by name\n" +
		"- \"notebook jupyter\" — keyword search, up to max_results best matches\n" +
		"- \"+slack send\" — require \"slack\" in the name, rank by the remaining terms\n\n" +
		"Each match is returned as one <function>{...}</function> line inside a <functions> block."
}

func (ToolSearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Query: 'select:A,B', keywords, or '+required rest'"},
			"max_results": {"type": "integer", "description": "Max matches to return (default 5)"}
		},
		"required": ["query"]
	}`)
}

func (t ToolSearchTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if t.Reg == nil {
		return Result{IsError: true, Output: "tool registry unavailable"}, nil
	}
	if strings.TrimSpace(in.Query) == "" {
		return Result{IsError: true, Output: "query is required"}, nil
	}
	if in.MaxResults <= 0 {
		in.MaxResults = 5
	}

	all := t.Reg.All()

	var matched []Tool
	if rest, ok := strings.CutPrefix(in.Query, "select:"); ok {
		// Exact name selection.
		want := map[string]bool{}
		for _, n := range strings.Split(rest, ",") {
			want[strings.TrimSpace(n)] = true
		}
		for _, tl := range all {
			if want[tl.Name()] {
				matched = append(matched, tl)
			}
		}
	} else {
		// Keyword ranking. A leading "+term" makes term a required name substring.
		var required string
		terms := strings.Fields(strings.ToLower(in.Query))
		var kw []string
		for _, w := range terms {
			if r, ok := strings.CutPrefix(w, "+"); ok {
				required = r
			} else {
				kw = append(kw, w)
			}
		}
		type scored struct {
			t     Tool
			score int
		}
		var ranked []scored
		for _, tl := range all {
			name := strings.ToLower(tl.Name())
			if required != "" && !strings.Contains(name, required) {
				continue
			}
			hay := name + " " + strings.ToLower(tl.Description())
			s := 0
			for _, w := range kw {
				if strings.Contains(name, w) {
					s += 3 // name matches weigh more
				} else if strings.Contains(hay, w) {
					s++
				}
			}
			if required != "" {
				s += 2
			}
			if s > 0 {
				ranked = append(ranked, scored{tl, s})
			}
		}
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
		for _, r := range ranked {
			matched = append(matched, r.t)
		}
	}

	if len(matched) == 0 {
		return Result{Output: "<functions>\n(no tools matched)\n</functions>"}, nil
	}
	if len(matched) > in.MaxResults {
		matched = matched[:in.MaxResults]
	}
	var b strings.Builder
	b.WriteString("<functions>\n")
	for _, tl := range matched {
		def := map[string]any{
			"description": tl.Description(),
			"name":        tl.Name(),
			"parameters":  json.RawMessage(tl.InputSchema()),
		}
		line, _ := json.Marshal(def)
		fmt.Fprintf(&b, "<function>%s</function>\n", line)
	}
	b.WriteString("</functions>")
	return Result{Output: b.String()}, nil
}
