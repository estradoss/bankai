package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/estradoss/bankai/internal/codex"
	"github.com/estradoss/bankai/internal/commands"
	"github.com/estradoss/bankai/internal/config"
	"github.com/estradoss/bankai/internal/engine"
	"github.com/estradoss/bankai/internal/goal"
	"github.com/estradoss/bankai/internal/lsp"
	"github.com/estradoss/bankai/internal/mcp"
	"github.com/estradoss/bankai/internal/memory"
	"github.com/estradoss/bankai/internal/permission"
	"github.com/estradoss/bankai/internal/plugins"
	"github.com/estradoss/bankai/internal/provider"
	"github.com/estradoss/bankai/internal/session"
	"github.com/estradoss/bankai/internal/skills"
	"github.com/estradoss/bankai/internal/task"
	"github.com/estradoss/bankai/internal/tools"
	"github.com/estradoss/bankai/internal/transcript"
	"github.com/estradoss/bankai/internal/tui"
)

const version = "0.1.0"

type opts struct {
	prompt    string
	printMode bool
	cont      bool
	resume    string
	sessionID string
	model     string
	permMode  string
	sandbox   bool
	tui       bool
	help      bool
	ver       bool
}

func parseArgs(args []string) (opts, error) {
	fs := flag.NewFlagSet("bankai", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o opts
	fs.StringVar(&o.prompt, "p", "", "one-shot prompt; prints reply and exits (compat with claude -p)")
	fs.StringVar(&o.prompt, "print", "", "same as -p")
	fs.BoolVar(&o.cont, "c", false, "continue the most recent session in cwd")
	fs.BoolVar(&o.cont, "continue", false, "same as -c")
	fs.StringVar(&o.resume, "resume", "", "resume a specific session by uuid")
	fs.StringVar(&o.sessionID, "session-id", "", "start a new session with this uuid (interop with claude --session-id)")
	fs.StringVar(&o.model, "model", "", "override BANKAI_MODEL for this run")
	fs.StringVar(&o.permMode, "permission-mode", "", "permission mode: default|acceptEdits|bypassPermissions|dontAsk|plan")
	fs.BoolVar(&o.sandbox, "sandbox", false, "run Bash commands in an OS sandbox (no network, ro fs except cwd/tmp)")
	fs.BoolVar(&o.tui, "tui", false, "use the rich Bubbletea TUI instead of the line REPL")
	fs.BoolVar(&o.help, "h", false, "help")
	fs.BoolVar(&o.help, "help", false, "help")
	fs.BoolVar(&o.ver, "v", false, "version")
	fs.BoolVar(&o.ver, "version", false, "version")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.prompt != "" {
		o.printMode = true
	} else if fs.NArg() > 0 {
		// support `bankai "prompt words"` positional as oneshot too
		o.prompt = strings.Join(fs.Args(), " ")
		o.printMode = true
	}
	return o, nil
}

func main() {
	// Codex OAuth subcommands: `bankai codex login|logout`.
	if len(os.Args) >= 2 && os.Args[1] == "codex" {
		if err := runCodex(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "bankai:", err)
			os.Exit(1)
		}
		return
	}
	o, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "bankai:", err)
		os.Exit(2)
	}
	if o.ver {
		fmt.Println("bankai", version)
		return
	}
	if o.help {
		printHelp()
		return
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "bankai:", err)
		os.Exit(1)
	}
}

func run(o opts) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if o.model != "" {
		cfg.Model = o.model
	}
	sess, err := session.New(cfg.DataDir)
	if err != nil {
		return err
	}
	goals := goal.NewStore(sess.Dir)
	if err := goals.Load(); err != nil {
		return err
	}

	todos := tools.NewTodoStore()

	// Sub-agent tool registry: same file/search/exec tools, but no recursion
	// (no Task) and no goal mutation.
	sandboxWd, _ := os.Getwd()
	bashTool := tools.BashTool{Sandbox: o.sandbox, Workdir: sandboxWd}

	subReg := tools.NewRegistry()
	subReg.Register(bashTool)
	subReg.Register(tools.ReadTool{})
	subReg.Register(tools.EditTool{})
	subReg.Register(tools.WriteTool{})
	subReg.Register(tools.GlobTool{})
	subReg.Register(tools.GrepTool{})
	subReg.Register(tools.WebFetchTool{})
	subReg.Register(tools.WebSearchTool{})

	client := provider.NewClient(cfg.Auth, cfg.Model)
	if cfg.Codex != nil {
		client.OpenAI = provider.NewCodexClient(cfg.Codex.AccessToken, cfg.Codex.AccountID())
	}

	toolReg := tools.NewRegistry()
	toolReg.Register(bashTool)
	toolReg.Register(tools.ReadTool{})
	toolReg.Register(tools.EditTool{})
	toolReg.Register(tools.WriteTool{})
	toolReg.Register(tools.GlobTool{})
	toolReg.Register(tools.GrepTool{})
	toolReg.Register(tools.WebFetchTool{})
	toolReg.Register(tools.WebSearchTool{})
	toolReg.Register(tools.TodoWriteTool{Store: todos})
	toolReg.Register(tools.ExitPlanModeTool{})
	subRunner := engine.SubagentRunner(client, subReg, engine.ClaudeCodePrefix)
	toolReg.Register(tools.AgentTool{Run: subRunner})
	taskReg := task.NewRegistry(task.Runner(subRunner))
	toolReg.Register(tools.TaskCreateTool{Reg: taskReg})
	toolReg.Register(tools.TaskGetTool{Reg: taskReg})
	toolReg.Register(tools.TaskListTool{Reg: taskReg})
	toolReg.Register(tools.TaskOutputTool{Reg: taskReg})
	toolReg.Register(tools.TaskStopTool{Reg: taskReg})
	toolReg.Register(&tools.CreateGoalTool{Store: goals})
	toolReg.Register(&tools.UpdateGoalTool{Store: goals})
	toolReg.Register(&tools.GetGoalTool{Store: goals})

	home, _ := os.UserHomeDir()
	wd, _ := os.Getwd()

	// Plugins: ~/.claude/plugins/*/plugin.json. Each can contribute skills
	// (skills/ subdir) and MCP servers (manifest mcpServers), merged below.
	loadedPlugins := plugins.Load(home, nil)

	// Skills: user (~/.claude/skills) + project (.claude/skills) SKILL.md files,
	// plus any plugin skills/ dirs. Expose the Skill tool when any exist.
	skillSet := skills.Load(home, wd)
	for _, p := range loadedPlugins {
		if p.SkillsDir != "" {
			skillSet.AddPluginDir(p.SkillsDir)
		}
	}
	if skillSet.Len() > 0 {
		toolReg.Register(tools.SkillTool{Set: skillSet})
	}

	// MCP: dial configured stdio servers and bridge their tools in. Failures
	// are reported but non-fatal so a bad server doesn't block startup.
	mcpConfigs := mcp.LoadConfigs(home, wd)
	for name, cfg := range plugins.CollectMCPServers(loadedPlugins) {
		if _, exists := mcpConfigs[name]; !exists {
			mcpConfigs[name] = cfg
		}
	}
	var mcpMgr *mcp.Manager
	if len(mcpConfigs) > 0 {
		mgr, bridged, errs := mcp.Start(context.Background(), mcpConfigs)
		mcpMgr = mgr
		tools.RegisterMCPTools(toolReg, bridged)
		if len(bridged) > 0 {
			fmt.Fprintf(os.Stderr, "mcp: %d tool(s) from %d server(s)\n", len(bridged), len(mcpConfigs)-len(errs))
		}
		for name, err := range errs {
			fmt.Fprintf(os.Stderr, "mcp: server %q failed: %v\n", name, err)
		}
	}
	if mcpMgr != nil {
		defer mcpMgr.Close()
	}

	// Memory: file-based store under ~/.claude/projects/<sanitized-cwd>/memory.
	var memStore *memory.Store
	if projDir, err := transcript.ProjectDir(wd); err == nil {
		memStore = memory.NewStore(filepath.Join(projDir, "memory"))
		toolReg.Register(tools.CreateMemoryTool{Store: memStore})
		toolReg.Register(tools.SearchMemoryTool{Store: memStore})
		toolReg.Register(tools.DeleteMemoryTool{Store: memStore})
	}

	// LSP: language servers from settings.json lspServers + built-in defaults
	// (gopls). Servers start lazily on first diagnostics request.
	lspConfigs := lsp.LoadConfigs(home, wd)
	var lspMgr *lsp.Manager
	if len(lspConfigs) > 0 {
		lspMgr = lsp.NewManager(wd, lspConfigs)
		toolReg.Register(tools.LSPTool{Mgr: lspMgr})
		defer lspMgr.Close()
	}

	eng := engine.New(client, toolReg, goals)

	// Seed the session with the memory index so the model knows what it has
	// stored and can recall specifics via search_memory.
	if memStore != nil {
		if idx := memStore.Index(); idx != "" {
			eng.System += "\n\n# Persistent memory\nYou have durable memories from past sessions. Index:\n\n" +
				idx + "\nUse search_memory to read any memory's full content."
		}
	}

	// Permission gate. Rules come from ~/.claude/settings.json and the project's
	// .claude/settings.json(.local). Mode precedence: --permission-mode flag >
	// settings defaultMode > built-in default (bypass in one-shot, else default).
	allowRules, denyRules, settingsMode := permission.LoadSettings(home, wd)
	mode := permission.Mode(o.permMode)
	if o.permMode == "" {
		switch {
		case settingsMode.Valid():
			mode = settingsMode
		case o.printMode:
			mode = permission.ModeBypass
		default:
			mode = permission.ModeDefault
		}
	}
	if !mode.Valid() {
		return fmt.Errorf("invalid --permission-mode %q", o.permMode)
	}
	eng.Perms = permission.New(mode, allowRules, denyRules)

	cwd, _ := os.Getwd()
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}

	// Transcript setup + optional resume/continue.
	var priorSession string
	var priorPath string
	if o.cont {
		p, err := transcript.LatestSession(cwd)
		if err != nil {
			return err
		}
		if p == "" {
			return fmt.Errorf("no prior session found for %s", cwd)
		}
		priorPath = p
		priorSession = strings.TrimSuffix(filepath.Base(p), ".jsonl")
	} else if o.resume != "" {
		p, err := transcript.FindSession(cwd, o.resume)
		if err != nil {
			return err
		}
		priorPath = p
		priorSession = o.resume
	}

	writerID := priorSession
	if writerID == "" {
		writerID = o.sessionID // may still be empty → writer generates new uuid
	}
	tw, err := transcript.New(cwd, writerID)
	if err != nil {
		return err
	}
	eng.Transcript = tw

	if priorPath != "" {
		res, err := transcript.Load(priorPath)
		if err != nil {
			return fmt.Errorf("load transcript: %w", err)
		}
		eng.Messages = res.Messages
		tw.SetParent(res.LastUUID)
	}

	if o.printMode {
		return oneShot(context.Background(), eng, o.prompt)
	}

	cmdReg := commands.NewRegistry()
	cmdReg.Register(commands.Clear{})
	cmdReg.Register(commands.Exit{})
	cmdReg.Register(commands.Model{})
	cmdReg.Register(commands.Dump{})
	cmdReg.Register(commands.GoalCmd{})
	cmdReg.Register(commands.Compact{})
	cmdReg.Register(commands.Cost{})
	cmdReg.Register(commands.ContextCmd{})
	cmdReg.Register(commands.Todos{Store: todos})
	cmdReg.Register(commands.Plan{})
	cmdReg.Register(commands.Permissions{})
	cmdReg.Register(commands.Limits{})
	cmdReg.Register(commands.MCP{})
	cmdReg.Register(commands.PWD{})
	cmdReg.Register(commands.Tools{})
	cmdReg.Register(commands.System{})
	var pluginLines []string
	for _, p := range loadedPlugins {
		v := p.Version
		if v == "" {
			v = "?"
		}
		pluginLines = append(pluginLines, fmt.Sprintf("%s@%s — %s", p.Name, v, p.Description))
	}
	cmdReg.Register(commands.Plugins{Lines: pluginLines})
	if memStore != nil {
		cmdReg.Register(commands.Memory{Index: memStore.Index})
	}
	cmdReg.Register(commands.Init{})
	cmdReg.Register(commands.Commit{})
	cmdReg.Register(commands.Review{})
	cmdReg.Register(commands.Doctor{Source: cfg.Source})
	cmdReg.Register(commands.Help{Registry: cmdReg})

	ctx, cancel := signalCtx()
	defer cancel()

	fmt.Fprintf(os.Stderr, "bankai %s — auth=%s session=%s (%s)\n",
		version, cfg.Source, tw.SessionID, tw.Path)

	if o.tui {
		return tui.NewBubble(ctx, eng, cmdReg, goals).Run()
	}
	repl := tui.New(eng, cmdReg, goals)
	return repl.Run(ctx)
}

func runCodex(args []string) error {
	sub := "login"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "login":
		toks, err := codex.Login()
		if err != nil {
			return err
		}
		fmt.Printf("Codex login complete. Account %s. Run with CLAUDE_CODE_USE_OPENAI=1.\n", toks.AccountID)
		return nil
	case "logout":
		if err := codex.Logout(); err != nil {
			return err
		}
		fmt.Println("Codex credentials removed.")
		return nil
	default:
		return fmt.Errorf("unknown codex subcommand %q (use: login | logout)", sub)
	}
}

func oneShot(ctx context.Context, eng *engine.Engine, prompt string) error {
	eng.OnText = func(chunk string) { fmt.Print(chunk) }
	if err := eng.Submit(ctx, prompt); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func signalCtx() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()
	return ctx, cancel
}

func printHelp() {
	fmt.Println(`bankai — terminal coding agent (Go)

Usage:
  bankai                        interactive REPL, new session
  bankai -c                     continue most recent session in this cwd
  bankai --resume <uuid>        resume specific session
  bankai --session-id <uuid>    new session with a chosen uuid
  bankai -p "<prompt>"          one-shot; print reply and exit
  bankai "<prompt words>"       same as -p
  bankai --model <name>         override model for this run
  bankai --tui                  rich Bubbletea TUI (default is the line REPL)

Interop:
  Sessions live at ~/.claude/projects/<sanitized-cwd>/<uuid>.jsonl —
  the exact same file Claude Code uses. bankai and claude can hand a
  session back and forth: run one, exit, run the other with -c/--resume.

Providers:
  bankai codex login            log in to OpenAI Codex (subscription OAuth)
  bankai codex logout           remove Codex credentials
  CLAUDE_CODE_USE_OPENAI=1      route to Codex (Responses API) after login
  CLAUDE_CODE_USE_FOUNDRY=1     use ANTHROPIC_FOUNDRY_API_KEY
  ANTHROPIC_BASE_URL            point the Anthropic path at a gateway

Env:
  CLAUDE_CODE_OAUTH_TOKEN   override OAuth access token
  ANTHROPIC_API_KEY         used when no OAuth creds found
  BANKAI_MODEL              default model (default: ` + config.DefaultModel + `)

Slash commands (REPL):
  /help /goal /model /clear /dump /compact /cost /context
  /todos /plan /permissions /limits /mcp /memory /pwd /tools /system /plugins
  /init /commit /review /doctor /exit

Permissions:
  --permission-mode <m>     default|acceptEdits|bypassPermissions|dontAsk|plan
                            (interactive defaults to 'default'; -p defaults to bypass)
  --sandbox                 run Bash in an OS sandbox (bwrap/sandbox-exec):
                            no network, read-only fs except cwd + /tmp
  /permissions [mode]       show or switch mode at runtime`)
}
