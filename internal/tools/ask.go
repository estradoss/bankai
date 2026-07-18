package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// AskOption is one selectable choice.
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// AskQuestion is a single multiple-choice question.
type AskQuestion struct {
	Header      string      `json:"header"`
	Question    string      `json:"question"`
	Options     []AskOption `json:"options"`
	MultiSelect bool        `json:"multiSelect"`
}

// AskAnswer is the user's selection for one question.
type AskAnswer struct {
	Header   string   `json:"header"`
	Question string   `json:"question"`
	Selected []string `json:"selected"`
}

// AskPrompter presents questions to the user and returns their selections. A
// nil prompter (non-interactive session) makes the tool report that it cannot
// prompt so the model proceeds with sensible defaults.
type AskPrompter func(ctx context.Context, questions []AskQuestion) ([]AskAnswer, error)

// AskBridge is a settable holder for the interactive prompter. The tool is
// registered before the REPL exists, so the REPL wires its prompter here after
// construction.
type AskBridge struct{ Prompter AskPrompter }

// AskUserQuestionTool asks the user multiple-choice questions to gather
// information, clarify ambiguity, or offer choices. Ported from vibelearn's
// AskUserQuestionTool.
type AskUserQuestionTool struct{ Bridge *AskBridge }

func (AskUserQuestionTool) Name() string { return "AskUserQuestion" }

func (AskUserQuestionTool) Description() string {
	return "Ask the user multiple-choice questions to gather information, clarify ambiguity, understand " +
		"preferences, or offer choices. 1-4 questions, each with 2-4 options. Set multiSelect for non-exclusive " +
		"choices. If you recommend an option, put it first and append \"(Recommended)\" to its label. An \"Other\" " +
		"free-text choice is offered automatically — do not add one."
}

func (AskUserQuestionTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"questions": {
				"type": "array",
				"minItems": 1,
				"maxItems": 4,
				"description": "Questions to ask the user (1-4)",
				"items": {
					"type": "object",
					"properties": {
						"header": {"type": "string", "description": "Very short chip label (<=12 chars)"},
						"question": {"type": "string", "description": "The complete question, ending with '?'"},
						"multiSelect": {"type": "boolean", "description": "Allow selecting multiple options"},
						"options": {
							"type": "array",
							"minItems": 2,
							"maxItems": 4,
							"items": {
								"type": "object",
								"properties": {
									"label": {"type": "string", "description": "Concise choice text (1-5 words)"},
									"description": {"type": "string", "description": "What this option means / its trade-off"}
								},
								"required": ["label", "description"]
							}
						}
					},
					"required": ["question", "header", "options"]
				}
			}
		},
		"required": ["questions"]
	}`)
}

func (t AskUserQuestionTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Questions []AskQuestion `json:"questions"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if len(in.Questions) == 0 {
		return Result{IsError: true, Output: "at least one question is required"}, nil
	}
	if len(in.Questions) > 4 {
		return Result{IsError: true, Output: "at most 4 questions allowed"}, nil
	}
	for i, q := range in.Questions {
		if q.Question == "" {
			return Result{IsError: true, Output: fmt.Sprintf("question %d: question text is required", i+1)}, nil
		}
		if len(q.Options) < 2 || len(q.Options) > 4 {
			return Result{IsError: true, Output: fmt.Sprintf("question %d: must have 2-4 options", i+1)}, nil
		}
	}
	if t.Bridge == nil || t.Bridge.Prompter == nil {
		return Result{IsError: true, Output: "cannot prompt the user in this session; proceed with sensible defaults and state the assumption you made"}, nil
	}
	answers, err := t.Bridge.Prompter(ctx, in.Questions)
	if err != nil {
		return Result{IsError: true, Output: "asking user failed: " + err.Error()}, nil
	}
	// Render a compact human-readable summary alongside JSON for the model.
	var b strings.Builder
	for _, a := range answers {
		fmt.Fprintf(&b, "%s: %s\n", a.Header, strings.Join(a.Selected, ", "))
	}
	payload, _ := json.Marshal(map[string]any{"answers": answers})
	return Result{Output: strings.TrimSpace(b.String()) + "\n" + string(payload)}, nil
}
