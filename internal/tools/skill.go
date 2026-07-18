package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/estradoss/bankai/internal/skills"
)

// SkillTool lets the model invoke a packaged skill by name: its markdown body
// (a set of instructions for a particular kind of task) is returned for the
// model to follow. This is the Go port of vibelearn's SkillTool. Available
// skills are enumerated in the tool description so the model knows what exists.
type SkillTool struct{ Set *skills.Set }

func (SkillTool) Name() string { return "Skill" }

func (t SkillTool) Description() string {
	base := "Invoke a packaged skill by name. A skill is a set of instructions for a particular kind of task; invoking it loads those instructions for you to follow. Pass the skill's exact name."
	if t.Set == nil || t.Set.Len() == 0 {
		return base + " (No skills are currently available.)"
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\nAvailable skills:")
	for _, sk := range t.Set.List() {
		fmt.Fprintf(&b, "\n- %s: %s", sk.Name, sk.Description)
	}
	return b.String()
}

func (SkillTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "Exact name of the skill to invoke"},
			"args": {"type": "string", "description": "Optional arguments passed through to the skill"}
		},
		"required": ["name"]
	}`)
}

func (t SkillTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Name string `json:"name"`
		Args string `json:"args"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Name == "" {
		return Result{IsError: true, Output: "name is required"}, nil
	}
	if t.Set == nil {
		return Result{IsError: true, Output: "skills are not configured"}, nil
	}
	sk, ok := t.Set.Get(in.Name)
	if !ok {
		var names []string
		for _, s := range t.Set.List() {
			names = append(names, s.Name)
		}
		avail := "none"
		if len(names) > 0 {
			avail = strings.Join(names, ", ")
		}
		return Result{IsError: true, Output: fmt.Sprintf("unknown skill %q. Available: %s", in.Name, avail)}, nil
	}
	if sk.Body == "" {
		return Result{Output: fmt.Sprintf("Skill %q has no instructions.", sk.Name)}, nil
	}
	out := fmt.Sprintf("# Skill: %s\n\n%s", sk.Name, sk.Body)
	if strings.TrimSpace(in.Args) != "" {
		out += fmt.Sprintf("\n\n## Input\n\n%s", in.Args)
	}
	return Result{Output: out}, nil
}
