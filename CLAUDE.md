# bankai — Go rewrite of learnvibe/free-code (Claude Code fork)

Terminal coding agent. Go port. Currently a thin slice of the TypeScript original
(`_vibelearn/learnvibe`). Anthropic-only backend + Claude Code JSONL session interop +
the original `/goal` persistent-objective engine.

> **Active initiative:** migrating the LLM transport + tool loop to
> `charm.land/fantasy` to make the code leaner (Anthropic-only for now; drops
> `internal/codex`, replaces `internal/provider` SSE + `internal/engine` loop).
> OAuth-through-fantasy already proven in `tmp/pilot`. See **`PLAN.md`**.

## What exists today
- Agent loop (`internal/engine`): stream → tool_use dispatch → repeat.
- Backend: Anthropic Messages API streaming only (`internal/provider/anthropic.go`).
- Auth (`internal/auth`): OAuth env → macOS keychain → file → `ANTHROPIC_API_KEY`, auto-refresh.
- Tools (`internal/tools`): Bash, Read, Edit, Write, Glob, Grep, Web*, Todo, Task*, Skill, MCP, LSP,
  memory, NotebookEdit, Sleep, EnterWorktree/ExitWorktree, AskUserQuestion (REPL menu prompter),
  ToolSearch, Config, REPL (persistent python), lsp_diagnostics/hover/definition/rename,
  ide_selection/ide_open/ide_diff, transcribe (voice), create_goal/update_goal/get_goal.
- `/goal` engine (`internal/goal`): persistent objective, token budget, continuation prompts.
- Transcript interop (`internal/transcript`): Claude-Code-compatible JSONL, `-c` / `--resume`.
- Slash cmds: /help /goal /model /clear /dump /exit.

## Roadmap — port from `_vibelearn/learnvibe`

Priority order. Each maps to TS source under `_vibelearn/learnvibe/src`.

### Phase 1 — DONE
- [x] **Glob + Grep tools** — `internal/tools/glob.go`, `grep.go`. Grep uses ripgrep if present, else Go fallback.
- [x] **WebFetch + WebSearch tools** — `internal/tools/web.go`. WebFetch strips HTML; WebSearch via DuckDuckGo HTML.
- [x] **Subagents (Task tool)** — `internal/tools/agent.go` + `engine.SubagentRunner`. Synchronous, isolated
      sub-engine sharing the client + a recursion-free tool set. (Async/background Task* still deferred.)
- [x] **TodoWrite + plan mode** — `internal/tools/todo.go`, `plan.go` (ExitPlanMode) + `/plan` command.
- [x] **Context compaction** — `engine.Compact` + `/compact`, plus auto-compact at ~150k tokens (`AutoCompactChars`).
- [x] **Core slash cmds** — `/init /commit /review /compact /context /cost /doctor /plan /todos` in
      `internal/commands/more.go`. (/resume already existed as a `--resume` flag.)
- [x] **Providers**: OpenAI Codex (Responses API, subscription OAuth) + Anthropic base-URL/Foundry override —
      `internal/provider`, `internal/codex`. Codex login: `bankai codex login`; route with `CLAUDE_CODE_USE_OPENAI=1`.
      Also `ANTHROPIC_BASE_URL` / `CLAUDE_CODE_USE_FOUNDRY=1`.
- [x] **Cost / usage tracking** — `engine.TotalUsage`/`Turns` + `/cost`.

### Remaining — port ALL of it (target: full parity)

Nothing is deferred. Everything below is planned work toward feature parity with vibelearn.
Rough dependency order (do top-down; later items lean on earlier infra).

1. [x] **Permission gate** — `internal/permission`. Modes (default/acceptEdits/bypassPermissions/
       dontAsk/plan), deny>allow>mode-default rule precedence, content-match rules, interactive
       asker (y/always/no) wired through the REPL. Gate in `engine.Perms`; `--permission-mode` flag +
       `/permissions` cmd; `/plan` hard-engages plan mode. Rules + defaultMode load from
       `~/.claude/settings.json` and project `.claude/settings.json(.local)` (Claude-Code
       `Bash(git:*)` rule syntax, substring subset). Sandbox toggle done: `--sandbox` runs Bash
       under bwrap (Linux) / sandbox-exec (macOS) — no network, ro fs except cwd+/tmp; fails
       CLOSED (never silently unsandboxed) when no backend. `internal/tools/bash_sandbox.go`.
2. [x] **Async/background Task management** — `internal/task` registry (goroutine per task,
       status running/completed/failed/stopped, cancellable via ctx) + `TaskCreate/TaskGet/
       TaskList/TaskOutput/TaskStop` tools. Reuses the SubagentRunner. Complements the synchronous
       `Task` tool. (Persistent cron/remote task kinds still TODO.)
3. [x] **Real TUI** — `internal/tui/bubble.go`: Bubbletea/lipgloss TUI (alt-screen viewport
       scrollback, textinput prompt, thinking spinner, model/perms/goal footer, modal permission
       prompt). Opt-in via `--tui`; line REPL stays the default fallback. Engine runs in a tea.Cmd
       goroutine, streams via p.Send, asker round-trips through a channel. Tool-call panels
       (`engine.OnToolStart` → `toolMsg`, colored ⚙ line per call); themes (`internal/theme`: 6
       palettes, `/theme`, settings-persisted, `ApplyTheme`); vim modal editing (`/vim`,
       settings `editorMode`, normal/insert with h/l/0/$/i/a/A/I/x/dd). NOTE: go.mod at 1.24 for
       bubbletea (per user decision).
4. [x] **Rate-limit / billing header display** — `provider.RateLimit` captures anthropic-ratelimit-*
       (requests/tokens/unified + retry-after) headers off every response; `/limits` command prints
       them, and the Bubbletea footer shows live remaining budget/tokens when known.
5. [x] **MCP client** — `internal/mcp`: JSON-RPC 2.0 over two transports — stdio (spawned command)
       and streamable HTTP (`http.go`: POST + application/json or text/event-stream SSE responses,
       Mcp-Session-Id continuity, static Authorization header). OAuth 2.1 flow (`oauth.go`,
       `oauth_browser.go`): protected-resource (RFC 9728) → auth-server (RFC 8414) metadata
       discovery → dynamic client registration (RFC 7591) → PKCE/S256 authorization-code exchange
       (RFC 7636), with a loopback browser Authorizer (opens browser + local callback server);
       `auth:"oauth"` in a server config triggers it. Config loader (mcpServers from user+project
       settings.json; `type`+`url` select HTTP, `command` selects stdio), Manager dials all servers
       non-fatally and bridges tools as `mcp__<server>__<tool>` (`tools.MCPTool`). `/mcp` lists them.
       Resources: `resources/list`+`resources/read` with `ListMcpResources`/`ReadMcpResource`.
6. [x] **LSP client** — `internal/lsp`: Content-Length-framed JSON-RPC client (initialize, didOpen,
       publishDiagnostics registry), Manager routing by file extension with lazy server start,
       config from settings.json lspServers + built-in gopls default. Tools: `lsp_diagnostics`,
       `lsp_hover`, `lsp_definition`, `lsp_rename` (applies the WorkspaceEdit to disk). Passive-
       feedback loop wired (`engine.LSPFeedback`): a clean Edit/Write auto-appends fresh
       diagnostics for the edited file to the tool result.
7. [x] **Memory subsystem** — `internal/memory`: file-based memdir store under
       `~/.claude/projects/<sanitized>/memory` (frontmatter md files: name/description/type
       user|feedback|project|reference, MEMORY.md index, keyword relevance search). Tools
       `create_memory/search_memory/delete_memory`; MEMORY.md index seeded into the system prompt;
       `/memory` command. Secret scanner done (`secrets.go`): `Store.Save` refuses to persist
       content matching credential patterns (AWS/GitHub/Slack/Google/OpenAI/Anthropic/Stripe keys,
       JWTs, private-key blocks, generic secret assignments). Auto-extract done
       (`engine.ExtractMemories` + `/memory-extract`: one model turn proposes durable memories from
       the conversation as JSON; proposals only, user approves). Dream done (`memory.Consolidate` +
       `/dream`: offline Jaccard clustering of near-duplicate memories, proposes merges). (Team
       memory sync is remote-transport — tracks under Remote, item 11.)
8. [x] **Skills system** — `internal/skills` loader (user `~/.claude/skills` + project
       `.claude/skills`, SKILL.md frontmatter parse, project overrides user) + `Skill` tool that
       enumerates skills in its description and returns a skill's body (+ optional `args`) on
       invocation. Bundled skills ported (`internal/skills/bundled.go`: simplify, stuck, skillify;
       user/project/plugin skills override by name). Remaining TS bundled skills skipped as
       infra-bound (batch=bg-worktree agents, loop/schedule=cron, lorem=dynamic ant-only gen).
       `ToolSearch` tool ported (`internal/tools/toolsearch.go`: select:/keyword/+required query
       forms, returns `<functions>` schema block; bankai exposes all tools directly so it is a
       discovery aid, not a deferral gate). DONE: core loader + Skill tool (+args) + 5 bundled
       skills (simplify/stuck/skillify/verify/remember) + ToolSearch. (Marketplace-installed skills
       track under Plugins item 9.)
9. [x] **Plugins** — `internal/plugins`: discovers ~/.claude/plugins/*/plugin.json (or
       .claude-plugin/plugin.json), each contributing skills (skills/ dir), MCP servers (manifest
       mcpServers, namespaced), **agents** (manifest `agents` → typed sub-agent types on the Task
       tool via `engine.SubagentRunnerTyped`), and **hooks** (manifest `hooks` → `engine.Hook`
       PostToolUse command runner, matcher regexp on tool name, JSON payload on stdin). Marketplace
       **install/update/remove** via git (`Install`/`Update`/`Remove`, `/plugin install <git-url>|
       update|remove|list`). Respects disabled list; `/plugins` lists loaded ones.
10. [SKIP] **Bedrock + Vertex providers** — AWS SigV4 + GCP ADC signing over the Anthropic Messages shape.
       `src/services/api/`. **Intentionally skipped** — not porting Bedrock/Vertex.
11. [x] **Remote / server / coordinator** — `internal/server`: stdlib HTTP+SSE remote server
       (`bankai --serve [--serve-port N] [--serve-token T]`), bearer auth, `/health`. Single-session
       route `POST /v1/message {"prompt"}` streams model text as SSE `message`/`done`/`error`.
       Multi-session **RemoteSessionManager** (`manager.go`): `POST /v1/sessions` spins a fresh
       engine via an injected factory, `GET /v1/sessions` lists ids, `POST /v1/sessions/{id}/message`
       routes a streamed turn. **Team memory sync** done: `memory.SyncClient` Push/Pull +
       `server.TeamMemory` shared `/v1/memory` endpoint (bearer-authed, merge-by-name), driven by
       `/memory-sync push|pull <url> [token]`; served under `--serve`. **Permission bridge** done:
       a remote turn's approval prompts are emitted as SSE `permission` frames and blocked on until
       the client resolves them via `POST /v1/permission {id,decision}` (allow_once/allow_always/
       deny), with ctx-cancel fallback to deny. **Upstream proxy/relay** done (`proxy.go`: `Proxy`
       forwards `/v1/message` to an upstream server and pipes the SSE stream through unbuffered;
       `StreamMessage` client helper consumes an SSE session). WebSocket transport intentionally
       omitted — HTTP+SSE is the chosen streaming transport (stdlib-only, no ws dependency); a WS
       variant would be a redundant alt-transport, not a new capability. `src/remote/`,
       `src/server/`, `src/coordinator/`.
12. [x] **Voice** — `internal/voice`: dictation/transcription — keyterm management (dedup, near-miss
       correction snapping transcribed words onto canonical keyterms via bounded Levenshtein), a
       dictation buffer, an injectable `Transcriber` (default `WhisperTranscriber` shelling to
       whisper/whisper.cpp), and **live mic capture / push-to-talk** (`CLIRecorder` via
       arecord/sox/ffmpeg → `Session.Dictate` records N seconds then transcribes). Agent tool
       `transcribe` + `/dictate [seconds]` command (submits the transcription as a turn), gated by
       VOICE_MODE. **Real-time streaming STT done** (`stream_stt.go` + `wsclient.go` +
       `stream_capture.go`): a stdlib-only RFC 6455 WebSocket client drives Anthropic's
       `voice_stream` endpoint (linear16/16kHz/mono binary frames, KeepAlive/CloseStream JSON
       control, TranscriptText/Endpoint/Error handling, interim→final promotion + finalize
       timeout ladder), fed raw PCM from arecord/sox/ffmpeg; exposed as the `transcribe_stream`
       tool (gated by VOICE_MODE + Anthropic OAuth) alongside the whisper batch path.
       `src/services/voice*`, `src/services/voiceStreamSTT.ts`.
13. [x] **IDE integration** — `internal/bridge`: HTTP bridge + discovery lockfile
       (`~/.claude/ide/<port>.lock`, run via `bankai --ide [--ide-port N]`). The editor pushes state
       (`POST /v1/selection`, `/v1/diagnostics`) and polls agent→IDE commands (`GET /v1/commands`);
       the agent reads/drives it through `ide_selection`/`ide_open`/`ide_diff` tools. Reference
       **VS Code extension** ships in `editors/vscode/` (auto-discovers the lockfile, shares the
       selection, applies open-file/show-diff — zero npm deps, Node built-ins). A JetBrains plugin
       would target the same three HTTP calls against the same lockfile (README documents it).
       `src/bridge/`.
14. [x] **Slash-command surface** — ~46 commands in `internal/commands`: the existing set plus
       `/permissions /limits /mcp /memory /pwd /tools /system /diff /status /export /release-notes
       /copy /theme /vim /plugin /dream /memory-extract /memory-sync /dictate /version /env /stats
       /usage /rewind /hooks /summary /security-review /effort /output-style` (base set: /help /goal
       /model /clear /dump /exit /compact /cost /context /todos /plan /features /plugins /init
       /commit /review /doctor). Every command that maps to a self-hosted Go CLI is ported. The
       remaining TS commands in `src/commands/` are hosted-product chrome that does not apply here —
       onboarding/install-github-app/install-slack-app/desktop/mobile/teleport/thinkback/statusline/
       stickers/color/keybindings/tag/passes/share/ant-trace/heapdump/backfill-sessions/break-cache/
       mock-limits/reset-limits/rate-limit-options/extra-usage/insights/advisor and similar
       telemetry, account, and IDE-chrome commands tied to the Anthropic-hosted product.
15. [x] **Feature-flag system** — `internal/feature`: runtime analogue of vibelearn's compile-time
       feature('FLAG'). Resolves flags from build defaults < BANKAI_FEATURES env < --feature CLI
       (FLAG/+FLAG/-FLAG/FLAG=0 token forms). Gates SKILLS/MCP/LSP/MEMORY/PLUGINS/TASKS/TUI
       subsystems (VOICE_MODE/BRIDGE_MODE/BEDROCK/VERTEX/REMOTE default off); `/features` lists
       state. Mechanism complete — Go uses runtime gating over compile-time bundling; individual
       unported features (voice/remote/etc.) track under their own roadmap items, not here.

16. [x] **Tool-level parity ports** — the remaining agent-facing tools from `src/tools/`
       not covered above, ported in bankai idiom and registered in `cmd/bankai/main.go`:
       **StructuredOutput** (SyntheticOutputTool; JSON-schema-validated final output),
       **EnterPlanMode** (`internal/tools/plan.go`; flips the permission gate to ModePlan,
       counterpart to ExitPlanMode), **TaskUpdate** (`todo.go`; mutate one todo in place),
       **SendUserMessage** (`brief.go`; BriefTool — emit a user-facing message + attachments),
       **McpAuth** (`mcpauth.go`; trigger a server's OAuth flow via `internal/mcp`),
       **Cron{Create,List,Delete}** (`cron.go` + new `internal/cron`: 5-field parser, next-run,
       durable `.claude/scheduled_tasks.json` store, minute-tick scheduler firing prompts as
       background tasks — gated by TASKS), **Workflow** (`workflow.go`; sequential/parallel
       multi-step sub-agent orchestration over the SubagentRunner — gated by TASKS),
       **SendMessage / RemoteTrigger / TeamCreate / TeamDelete** (`swarm.go`; inter-agent
       messaging + trigger rules via the clawx `master` CLI, local team files under
       `~/.claude/teams` — gated by REMOTE), and faithful disabled stubs **Tungsten** /
       **VerifyPlanExecution** (`stubs.go`; unavailable in this build, matching the TS originals).
       Still not ported (heavier / infra-bound): session `migrations/`, keybindings customization +
       full `outputStyles`, and the `brief`/`ultraplan`/`ultrareview` hosted commands.
       (Real-time `voiceStreamSTT` is now done — see item 12.)

17. [SKIP] **Buddy / companion sprite** — `src/buddy/` (companion.ts, sprites.ts, prompt.ts,
       CompanionSprite.tsx, useBuddyNotification.tsx). Cosmetic ink-TUI pet: seeded-gacha animal
       sprite beside the input box with an occasional speech bubble, gated by `feature('BUDDY')`.
       Pure UI chrome + cosmetics, no agent capability. **Intentionally skipped** — not porting.

See `_vibelearn/learnvibe/FEATURES.md` for the complete flag/subsystem inventory.

## Notes
- `_vibelearn/learnvibe` is reference source only — not wired into the Go build.
- Keep Claude Code JSONL interop compatible when adding features (sessions hand off to real `claude`).
- Build: `make build` (dist/bankai), `make install`, `make test`. Go 1.24+ (bumped from 1.22 for bubbletea).
- Debug: `BANKAI_DEBUG=1` dumps raw HTTP. Model: `BANKAI_MODEL` env / `--model` / `/model`.
