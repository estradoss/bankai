package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/estradoss/bankai/internal/bridge"
	"github.com/estradoss/bankai/internal/commands"
	"github.com/estradoss/bankai/internal/config"
	"github.com/estradoss/bankai/internal/cron"
	"github.com/estradoss/bankai/internal/engine"
	"github.com/estradoss/bankai/internal/feature"
	"github.com/estradoss/bankai/internal/goal"
	"github.com/estradoss/bankai/internal/lsp"
	"github.com/estradoss/bankai/internal/mcp"
	"github.com/estradoss/bankai/internal/memory"
	"github.com/estradoss/bankai/internal/permission"
	"github.com/estradoss/bankai/internal/plugins"
	"github.com/estradoss/bankai/internal/provider"
	"github.com/estradoss/bankai/internal/server"
	"github.com/estradoss/bankai/internal/session"
	"github.com/estradoss/bankai/internal/skills"
	"github.com/estradoss/bankai/internal/task"
	"github.com/estradoss/bankai/internal/theme"
	"github.com/estradoss/bankai/internal/tools"
	"github.com/estradoss/bankai/internal/transcript"
	"github.com/estradoss/bankai/internal/tui"
	"github.com/estradoss/bankai/internal/voice"
)

const version = "0.1.0"

type opts struct {
	prompt     string
	printMode  bool
	cont       bool
	resume     string
	sessionID  string
	model      string
	permMode   string
	sandbox    bool
	tui        bool
	serve      bool
	servePort  string
	serveToken string
	ide        bool
	idePort    int
	features   stringList
	help       bool
	ver        bool
}

// stringList collects a repeatable string flag (e.g. --feature X --feature Y).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
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
	fs.BoolVar(&o.serve, "serve", false, "expose the agent as a remote HTTP+SSE session instead of the REPL")
	fs.StringVar(&o.servePort, "serve-port", "8787", "port for --serve")
	fs.StringVar(&o.serveToken, "serve-token", "", "bearer token required by --serve clients (empty = no auth)")
	fs.BoolVar(&o.ide, "ide", false, "run the IDE bridge (lockfile + HTTP) so an editor extension can connect")
	fs.IntVar(&o.idePort, "ide-port", 8788, "port for the IDE bridge (--ide)")
	fs.Var(&o.features, "feature", "enable/disable a feature flag (repeatable): FLAG, -FLAG, FLAG=0")
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
	toolReg.Register(tools.TaskUpdateTool{Store: todos})
	toolReg.Register(tools.SendUserMessageTool{})
	toolReg.Register(tools.ExitPlanModeTool{})
	toolReg.Register(tools.StructuredOutputTool{})
	toolReg.Register(tools.TungstenTool{})
	toolReg.Register(tools.VerifyPlanExecutionTool{})
	toolReg.Register(tools.NotebookEditTool{})
	toolReg.Register(tools.SleepTool{})
	wtState := &tools.WorktreeState{}
	toolReg.Register(tools.EnterWorktreeTool{State: wtState})
	toolReg.Register(tools.ExitWorktreeTool{State: wtState})
	askBridge := &tools.AskBridge{}
	toolReg.Register(tools.AskUserQuestionTool{Bridge: askBridge})
	toolReg.Register(tools.ToolSearchTool{Reg: toolReg})
	toolReg.Register(&tools.REPLTool{})
	subReg.Register(tools.NotebookEditTool{})
	subReg.Register(tools.SleepTool{})
	subRunner := engine.SubagentRunner(client, subReg, engine.ClaudeCodePrefix)
	subRunnerTyped := engine.SubagentRunnerTyped(client, subReg, engine.ClaudeCodePrefix)
	feats := feature.Resolve(os.Getenv("BANKAI_FEATURES"), o.features)
	if feats.Enabled("TASKS") {
		taskReg := task.NewRegistry(task.Runner(subRunner))
		toolReg.Register(tools.TaskCreateTool{Reg: taskReg})
		toolReg.Register(tools.TaskGetTool{Reg: taskReg})
		toolReg.Register(tools.TaskListTool{Reg: taskReg})
		toolReg.Register(tools.TaskOutputTool{Reg: taskReg})
		toolReg.Register(tools.TaskStopTool{Reg: taskReg})

		// Scheduled tasks (cron): a fired job enqueues its prompt as a
		// background sub-agent via the same task registry. Durable tasks
		// persist under .claude/scheduled_tasks.json.
		cwd2, _ := os.Getwd()
		cronStore := cron.NewStore(cwd2, func(prompt string) {
			_, _ = taskReg.Create("scheduled task", prompt)
		})
		cronStore.Start()
		defer cronStore.Stop()
		toolReg.Register(tools.CronCreateTool{Store: cronStore})
		toolReg.Register(tools.CronListTool{Store: cronStore})
		toolReg.Register(tools.CronDeleteTool{Store: cronStore})

		// Workflow: multi-step sub-agent orchestration over the same runner.
		toolReg.Register(tools.WorkflowTool{Run: subRunner})
	}
	// Multi-agent / swarm tools (SendMessage, RemoteTrigger, Team*): gated by
	// REMOTE since they reach the clawx `master` CLI and shared team files.
	if feats.Enabled("REMOTE") {
		swarmHome, _ := os.UserHomeDir()
		toolReg.Register(tools.SendMessageTool{})
		toolReg.Register(tools.RemoteTriggerTool{})
		toolReg.Register(tools.TeamCreateTool{HomeDir: swarmHome})
		toolReg.Register(tools.TeamDeleteTool{HomeDir: swarmHome})
	}
	toolReg.Register(&tools.CreateGoalTool{Store: goals})
	toolReg.Register(&tools.UpdateGoalTool{Store: goals})
	toolReg.Register(&tools.GetGoalTool{Store: goals})

	home, _ := os.UserHomeDir()
	wd, _ := os.Getwd()
	toolReg.Register(tools.ConfigTool{HomeDir: home, ProjectDir: wd})

	// IDE bridge: editor state + agent→IDE commands. The ide_* tools always
	// exist (graceful when no editor is connected); --ide runs the HTTP bridge
	// and writes the discovery lockfile.
	ideBridge := bridge.New()
	toolReg.Register(tools.IDESelectionTool{Bridge: ideBridge})
	toolReg.Register(tools.IDEOpenTool{Bridge: ideBridge})
	toolReg.Register(tools.IDEDiffTool{Bridge: ideBridge})

	// Voice: dictation/transcription via a local STT backend, gated by VOICE_MODE.
	var voiceSession *voice.Session
	if feats.Enabled("VOICE_MODE") {
		voiceSession = voice.NewSession(nil)
		toolReg.Register(tools.TranscribeTool{Session: voiceSession})
		// Real-time streaming STT over Anthropic voice_stream — needs OAuth.
		if cfg.OAuth != nil {
			toolReg.Register(tools.StreamTranscribeTool{
				Session: voiceSession,
				Token:   cfg.OAuth.AccessToken,
			})
		}
	}
	if o.ide {
		if lock, err := bridge.WriteLockfile(home, o.idePort, o.serveToken, []string{wd}); err == nil {
			defer bridge.RemoveLockfile(home, o.idePort)
			fmt.Fprintf(os.Stderr, "IDE bridge on :%d (lockfile %s)\n", o.idePort, lock)
			go func() { _ = http.ListenAndServe(fmt.Sprintf(":%d", o.idePort), ideBridge.Handler()) }()
		}
	}

	// Plugins: ~/.claude/plugins/*/plugin.json. Each can contribute skills
	// (skills/ subdir) and MCP servers (manifest mcpServers), merged below.
	var loadedPlugins []plugins.Plugin
	if feats.Enabled("PLUGINS") {
		loadedPlugins = plugins.Load(home, nil)
	}

	// Task tool with any plugin-contributed agent types.
	agentDefs := map[string]tools.AgentDef{}
	for _, a := range plugins.CollectAgents(loadedPlugins) {
		agentDefs[a.Name] = tools.AgentDef{Name: a.Name, Description: a.Description, Prompt: a.Prompt}
	}
	toolReg.Register(tools.AgentTool{Run: subRunner, RunTyped: subRunnerTyped, Agents: agentDefs})

	// Skills: user (~/.claude/skills) + project (.claude/skills) SKILL.md files,
	// plus any plugin skills/ dirs. Expose the Skill tool when any exist.
	skillSet := skills.Load(home, wd)
	for _, p := range loadedPlugins {
		if p.SkillsDir != "" {
			skillSet.AddPluginDir(p.SkillsDir)
		}
	}
	skillSet.AddBundled() // built-in skills; user/project/plugin skills override on name
	if feats.Enabled("SKILLS") && skillSet.Len() > 0 {
		toolReg.Register(tools.SkillTool{Set: skillSet})
	}

	// MCP: dial configured stdio servers and bridge their tools in. Failures
	// are reported but non-fatal so a bad server doesn't block startup.
	mcpConfigs := map[string]mcp.ServerConfig{}
	if feats.Enabled("MCP") {
		mcpConfigs = mcp.LoadConfigs(home, wd)
		for name, cfg := range plugins.CollectMCPServers(loadedPlugins) {
			if _, exists := mcpConfigs[name]; !exists {
				mcpConfigs[name] = cfg
			}
		}
	}
	var mcpMgr *mcp.Manager
	if len(mcpConfigs) > 0 {
		toolReg.Register(tools.McpAuthTool{Configs: mcpConfigs})
		mgr, bridged, errs := mcp.Start(context.Background(), mcpConfigs)
		mcpMgr = mgr
		tools.RegisterMCPTools(toolReg, bridged)
		if len(mgr.Resources()) > 0 {
			toolReg.Register(tools.ListMcpResourcesTool{Mgr: mgr})
			toolReg.Register(tools.ReadMcpResourceTool{Mgr: mgr})
		}
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
	if feats.Enabled("MEMORY") {
		if projDir, err := transcript.ProjectDir(wd); err == nil {
			memStore = memory.NewStore(filepath.Join(projDir, "memory"))
			toolReg.Register(tools.CreateMemoryTool{Store: memStore})
			toolReg.Register(tools.SearchMemoryTool{Store: memStore})
			toolReg.Register(tools.DeleteMemoryTool{Store: memStore})
		}
	}

	// LSP: language servers from settings.json lspServers + built-in defaults
	// (gopls). Servers start lazily on first diagnostics request.
	var lspConfigs map[string]lsp.ServerConfig
	if feats.Enabled("LSP") {
		lspConfigs = lsp.LoadConfigs(home, wd)
	}
	var lspMgr *lsp.Manager
	if len(lspConfigs) > 0 {
		lspMgr = lsp.NewManager(wd, lspConfigs)
		toolReg.Register(tools.LSPTool{Mgr: lspMgr})
		toolReg.Register(tools.LSPHoverTool{Mgr: lspMgr})
		toolReg.Register(tools.LSPDefinitionTool{Mgr: lspMgr})
		toolReg.Register(tools.LSPRenameTool{Mgr: lspMgr})
		defer lspMgr.Close()
	}

	eng := engine.New(client, toolReg, goals)

	// Plugin-contributed hooks run on tool-call events (e.g. PostToolUse).
	for _, h := range plugins.CollectHooks(loadedPlugins) {
		event := h.Event
		if event == "" {
			event = "PostToolUse"
		}
		eng.Hooks = append(eng.Hooks, engine.Hook{Event: event, Matcher: h.Matcher, Command: h.Command})
	}

	// LSP passive-feedback: after a clean Edit/Write, surface fresh diagnostics.
	if lspMgr != nil {
		eng.LSPFeedback = func(ctx context.Context, filePath string) string {
			diags, err := lspMgr.Diagnose(ctx, filePath)
			if err != nil || len(diags) == 0 {
				return ""
			}
			var b strings.Builder
			for _, d := range diags {
				fmt.Fprintf(&b, "  %d:%d %s: %s\n",
					d.Range.Start.Line+1, d.Range.Start.Character+1, lsp.SeverityName(d.Severity), d.Message)
			}
			return strings.TrimRight(b.String(), "\n")
		}
	}

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
	toolReg.Register(tools.EnterPlanModeTool{Perms: eng.Perms})

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
	cmdReg.Register(commands.Diff{})
	cmdReg.Register(commands.GitStatus{})
	cmdReg.Register(commands.Version{V: version})
	cmdReg.Register(commands.Env{})
	cmdReg.Register(commands.Stats{})
	cmdReg.Register(commands.Usage{})
	cmdReg.Register(commands.Rewind{})
	cmdReg.Register(commands.Hooks{})
	cmdReg.Register(commands.Summary{})
	cmdReg.Register(commands.SecurityReview{})
	cmdReg.Register(commands.Effort{})
	cmdReg.Register(commands.OutputStyle{})
	cmdReg.Register(commands.Export{})
	cmdReg.Register(commands.ReleaseNotes{})
	cmdReg.Register(commands.Copy{})
	cmdReg.Register(commands.Theme{})
	cmdReg.Register(commands.Vim{})
	cmdReg.Register(commands.Plugin{})
	if voiceSession != nil {
		cmdReg.Register(commands.Dictate{Session: voiceSession})
	}
	var pluginLines []string
	for _, p := range loadedPlugins {
		v := p.Version
		if v == "" {
			v = "?"
		}
		pluginLines = append(pluginLines, fmt.Sprintf("%s@%s — %s", p.Name, v, p.Description))
	}
	cmdReg.Register(commands.Plugins{Lines: pluginLines})
	cmdReg.Register(commands.Features{Flags: feats.List()})
	if memStore != nil {
		cmdReg.Register(commands.Memory{Index: memStore.Index})
		cmdReg.Register(commands.Dream{Store: memStore})
		cmdReg.Register(commands.MemoryExtract{Store: memStore})
		cmdReg.Register(commands.MemorySync{Store: memStore})
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

	// Active color theme from settings.json "theme" (default palette otherwise).
	if pal, ok := theme.Get(loadSetting(home, wd, "theme")); ok {
		tui.ApplyTheme(pal)
	}

	if o.serve {
		addr := ":" + o.servePort
		auth := "no auth"
		if o.serveToken != "" {
			auth = "bearer-token auth"
		}
		// Multi-session manager: each POST /v1/sessions spins up a fresh engine
		// sharing the client + tool registry. The initial `eng` backs the
		// single-session /v1/message route too.
		factory := func() server.Engine { return engine.New(client, toolReg, goals) }
		mgr := server.NewManager(factory, o.serveToken)
		mux := http.NewServeMux()
		mux.Handle("/v1/sessions", mgr.Handler())
		mux.Handle("/v1/sessions/", mgr.Handler())
		server.NewTeamMemory(o.serveToken).Register(mux) // team memory sync endpoint
		mux.Handle("/", server.New(eng, o.serveToken).Handler())
		fmt.Fprintf(os.Stderr, "bankai remote server on %s (%s) — POST /v1/message or /v1/sessions\n", addr, auth)
		return http.ListenAndServe(addr, mux)
	}

	// Rich Bubbletea TUI is the default on an interactive terminal; the line
	// REPL is the fallback for pipes/non-TTY, or when forced via BANKAI_NO_TUI.
	useTUI := feats.Enabled("TUI") && (o.tui || (isTTY() && os.Getenv("BANKAI_NO_TUI") == ""))
	if useTUI {
		banner := tui.BannerInfo{
			Version: version,
			User:    currentUser(),
			Cwd:     wd,
			Effort:  loadSetting(home, wd, "effort"),
		}
		bub := tui.NewBubbleWithBanner(ctx, eng, cmdReg, goals, banner)
		if loadSetting(home, wd, "editorMode") == "vim" {
			bub.SetVim(true)
		}
		return bub.Run()
	}
	repl := tui.New(eng, cmdReg, goals)
	repl.Ask = askBridge
	return repl.Run(ctx)
}

func oneShot(ctx context.Context, eng *engine.Engine, prompt string) error {
	eng.OnText = func(chunk string) { fmt.Print(chunk) }
	if err := eng.Submit(ctx, prompt); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

// isTTY reports whether stdout is an interactive terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// currentUser returns a friendly user name for the welcome banner.
func currentUser() string {
	if u, err := user.Current(); err == nil {
		if u.Username != "" {
			return u.Username
		}
	}
	return os.Getenv("USER")
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
  bankai --feature FLAG         toggle a feature (repeatable): FLAG, -FLAG, FLAG=0

Interop:
  Sessions live at ~/.claude/projects/<sanitized-cwd>/<uuid>.jsonl —
  the exact same file Claude Code uses. bankai and claude can hand a
  session back and forth: run one, exit, run the other with -c/--resume.

Providers:
  CLAUDE_CODE_USE_FOUNDRY=1     use ANTHROPIC_FOUNDRY_API_KEY
  ANTHROPIC_BASE_URL            point the Anthropic path at a gateway

Env:
  CLAUDE_CODE_OAUTH_TOKEN   override OAuth access token
  ANTHROPIC_API_KEY         used when no OAuth creds found
  BANKAI_MODEL              default model (default: ` + config.DefaultModel + `)

Slash commands (REPL):
  /help /goal /model /clear /dump /compact /cost /context
  /todos /plan /permissions /limits /mcp /memory /pwd /tools /system /plugins /features
  /init /commit /review /doctor /exit

Permissions:
  --permission-mode <m>     default|acceptEdits|bypassPermissions|dontAsk|plan
                            (interactive defaults to 'default'; -p defaults to bypass)
  --sandbox                 run Bash in an OS sandbox (bwrap/sandbox-exec):
                            no network, read-only fs except cwd + /tmp
  /permissions [mode]       show or switch mode at runtime`)
}

// loadSetting reads a top-level string setting from project then user
// settings.json (project wins). Empty when unset.
func loadSetting(home, wd, key string) string {
	read := func(p string) string {
		raw, err := os.ReadFile(p)
		if err != nil {
			return ""
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			return ""
		}
		if v, ok := m[key].(string); ok {
			return v
		}
		return ""
	}
	val := ""
	if home != "" {
		if v := read(filepath.Join(home, ".claude", "settings.json")); v != "" {
			val = v
		}
	}
	if wd != "" {
		if v := read(filepath.Join(wd, ".claude", "settings.json")); v != "" {
			val = v
		}
	}
	return val
}
