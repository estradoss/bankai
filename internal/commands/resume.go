package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/estradoss/bankai/internal/transcript"
)

// Resume loads a prior session's transcript into the live engine. With no args
// it lists recent sessions; with an id it resumes that one. In the Bubbletea
// TUI a bare `/resume` is intercepted to open an interactive picker instead.
type Resume struct{}

func (Resume) Name() string        { return "resume" }
func (Resume) Description() string { return "Resume a prior session (usage: /resume [id])" }

func (Resume) Run(ctx Context, args string) (Result, error) {
	cwd, _ := os.Getwd()
	id := strings.TrimSpace(args)
	if id == "" {
		items := recentSessionIDs(cwd, 12)
		if len(items) == 0 {
			return Result{Text: "no prior sessions to resume in this project"}, nil
		}
		var b strings.Builder
		b.WriteString("recent sessions (resume with /resume <id>):\n")
		for _, it := range items {
			fmt.Fprintf(&b, "  %s  %s\n", it.id, it.preview)
		}
		return Result{Text: strings.TrimRight(b.String(), "\n")}, nil
	}

	p, err := transcript.FindSession(cwd, id)
	if err != nil {
		return Result{}, err
	}
	res, err := transcript.Load(p)
	if err != nil {
		return Result{}, fmt.Errorf("load transcript: %w", err)
	}
	ctx.Engine.Messages = res.Messages
	if tw, err := transcript.New(cwd, id); err == nil {
		tw.SetParent(res.LastUUID)
		ctx.Engine.Transcript = tw
	}
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	return Result{Text: fmt.Sprintf("resumed session %s — %d messages loaded", short, len(res.Messages))}, nil
}

type sessionRow struct{ id, preview string }

// recentSessionIDs lists up to limit sessions for cwd's project, newest first.
func recentSessionIDs(cwd string, limit int) []sessionRow {
	dir, err := transcript.ProjectDir(cwd)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type se struct {
		id  string
		mod int64
	}
	var ss []se
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		ss = append(ss, se{id: strings.TrimSuffix(e.Name(), ".jsonl"), mod: info.ModTime().Unix()})
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].mod > ss[j].mod })
	if len(ss) > limit {
		ss = ss[:limit]
	}
	out := make([]sessionRow, 0, len(ss))
	for _, s := range ss {
		out = append(out, sessionRow{id: s.id, preview: transcript.FirstPrompt(filepath.Join(dir, s.id+".jsonl"))})
	}
	return out
}
