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

### Now / next — DONE
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

### Later (explicitly deferred)
- [ ] **Permission gate** — tools currently run UNRESTRICTED. Add before untrusted use. Sandbox toggle too.
- [ ] **IDE integration** — VS Code / JetBrains, selection, diff-in-IDE, bridge — `src/bridge/`.
- [ ] **Bedrock + Vertex providers** — need AWS SigV4 + GCP ADC signing; cloud-cred-gated, heavy.
      Anthropic-compatible gateways already work today via `ANTHROPIC_BASE_URL`.
- [ ] MCP client (connection mgr, OAuth, registry) — `src/services/mcp/`.
- [ ] LSP client (diagnostics) — `src/services/lsp/`.
- [ ] Memory subsystem (SessionMemory, extractMemories, autoDream, memdir) — `src/services/`.
- [ ] Skills system (bundled + user dir) — `src/skills/`.
- [ ] Plugins / marketplace — `src/services/plugins/`.
- [ ] Remote / server / coordinator (multi-agent, WebSocket sessions) — `src/remote/`, `src/server/`.
- [ ] Voice (STT, dictation) — `src/services/voice*`.
- [ ] Real TUI (Ink-equiv render engine, Vim mode, themes, keybindings) — `src/ink/`, `src/vim/`.
- [ ] Feature-flag build system (88 compile-time flags) — `scripts/build.ts`.
- [ ] Async/background Task management (Task Create/Get/List/Stop) — needs a scheduler/daemon; overlaps
      with the deferred Remote/coordinator work. Synchronous `Task` tool exists today.
- [ ] Rate-limit / billing header display — cosmetic, tied to the deferred Real TUI.

## Notes
- `_vibelearn/learnvibe` is reference source only — not wired into the Go build.
- Keep Claude Code JSONL interop compatible when adding features (sessions hand off to real `claude`).
- Build: `make build` (dist/bankai), `make install`, `make test`. Go 1.22+.
- Debug: `BANKAI_DEBUG=1` dumps raw HTTP. Model: `BANKAI_MODEL` env / `--model` / `/model`.
