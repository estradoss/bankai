# bankai — Go rewrite of learnvibe/free-code (Claude Code fork)

Terminal coding agent. Go port. Currently a thin slice of the TypeScript original
(`_vibelearn/learnvibe`). Anthropic-only backend + Claude Code JSONL session interop +
the original `/goal` persistent-objective engine.

## What exists today
- Agent loop (`internal/engine`): stream → tool_use dispatch → repeat.
- Backend: Anthropic Messages API streaming only (`internal/provider/anthropic.go`).
- Auth (`internal/auth`): OAuth env → macOS keychain → file → `ANTHROPIC_API_KEY`, auto-refresh.
- Tools (`internal/tools`): Bash, Read, Edit, Write, create_goal/update_goal/get_goal.
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
       `/permissions` cmd; `/plan` hard-engages plan mode. (Sandbox toggle still TODO.)
2. [ ] **Async/background Task management** — Task Create/Get/List/Output/Stop, a task registry + scheduler.
       `src/tasks/`. Upgrades today's synchronous `Task`.
3. [ ] **Real TUI** — Bubbletea/lipgloss equivalent of the Ink renderer: spinner, tool-call panels,
       streaming markdown, live todo/goal footer, themes, keybindings, Vim mode. `src/ink/`, `src/vim/`.
4. [ ] **Rate-limit / billing header display** — ride on the new TUI. `src/services/api/`, cost-tracker.
5. [ ] **MCP client** — connection manager, OAuth (xaa/idp), registry, transports, rich output,
       MCP tools (MCPTool/McpAuth/ListMcpResources/ReadMcpResource). `src/services/mcp/`.
6. [ ] **LSP client** — server manager, diagnostics registry, passive feedback, LSPTool. `src/services/lsp/`.
7. [ ] **Memory subsystem** — SessionMemory, extractMemories, autoDream, memdir, team memory sync +
       secret scanner, contextCollapse. `src/services/`, `src/memdir/`.
8. [ ] **Skills system** — bundled + user-dir skills, SkillTool, ToolSearch, skill loader. `src/skills/`.
9. [ ] **Plugins / marketplace** — install/manage, plugin CLI commands. `src/services/plugins/`.
10. [ ] **Bedrock + Vertex providers** — AWS SigV4 + GCP ADC signing over the Anthropic Messages shape.
       `src/services/api/`.
11. [ ] **Remote / server / coordinator** — WebSocket sessions, RemoteSessionManager, permission bridge,
       upstream proxy/relay, multi-agent coordinator. `src/remote/`, `src/server/`, `src/coordinator/`.
12. [ ] **Voice** — streaming STT, keyterms, push-to-talk, dictation. `src/services/voice*`.
13. [ ] **IDE integration** — VS Code / JetBrains bridge, selection, diff-in-IDE. `src/bridge/`.
14. [ ] **Full slash-command surface** — port the remaining ~110 commands from `src/commands/`.
15. [ ] **Feature-flag build system** — compile-time flag bundler equivalent to `scripts/build.ts`.

See `_vibelearn/learnvibe/FEATURES.md` for the complete flag/subsystem inventory.

## Notes
- `_vibelearn/learnvibe` is reference source only — not wired into the Go build.
- Keep Claude Code JSONL interop compatible when adding features (sessions hand off to real `claude`).
- Build: `make build` (dist/bankai), `make install`, `make test`. Go 1.22+.
- Debug: `BANKAI_DEBUG=1` dumps raw HTTP. Model: `BANKAI_MODEL` env / `--model` / `/model`.
