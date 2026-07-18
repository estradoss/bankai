# bankai (Go)

Terminal coding agent. **A Go port of [vibelearn / free-code](./_vibelearn/learnvibe)** — the
TypeScript Claude Code fork — reimplementing its core agent loop, tools, providers, and
commands in a single static Go binary. Also ships a persistent `/goal` command (original to
this port) inspired by [codex](https://github.com/openai/codex) and
[pi-goal](https://github.com/code-yeongyu/pi-goal).

**Goal: full parity.** The core agent surface is ported; the larger subsystems (real TUI, MCP,
LSP, memory, remote/coordinator, voice, plugins) are on the active roadmap, not deferred — see
[Port status](#port-status-vs-vibelearn).

## Interop with Claude Code

bankai reads Claude Code's OAuth token (`~/.claude/.credentials.json` or the
macOS keychain entry `Claude Code-credentials`) and writes transcripts to
`~/.claude/projects/<sanitized-cwd>/<uuid>.jsonl` — the same on-disk format
Claude Code uses. That means a session started by one CLI can be resumed by
the other:

```sh
cd /tmp/work
bankai -p "start it"        # creates session <uuid>
claude -c                   # picks up the same session, continues
bankai -c                   # picks it up again, still same session
```

Fallback auth: if no OAuth token is found, bankai falls back to
`ANTHROPIC_API_KEY`.

## Install

```sh
make install      # builds dist/bankai and symlinks ~/.local/bin/bankai
```

Requires Go 1.22+.

## Run

```sh
export ANTHROPIC_API_KEY=sk-ant-...
bankai
```

Optional env:
- `BANKAI_MODEL` — model id (default `claude-opus-4-7`)

### Providers

| backend | how to select |
|---------|---------------|
| Anthropic (default) | Claude OAuth or `ANTHROPIC_API_KEY` |
| Anthropic gateway | `ANTHROPIC_BASE_URL=<url>` |
| Anthropic Foundry | `CLAUDE_CODE_USE_FOUNDRY=1` + `ANTHROPIC_FOUNDRY_API_KEY` |
| OpenAI **Codex** | `bankai codex login` once, then `CLAUDE_CODE_USE_OPENAI=1` |

Codex uses the same subscription-OAuth + Responses API path as vibelearn's
`codex-fetch-adapter.ts` (PKCE login on port 1455, `chatgpt.com/backend-api/codex/responses`).

## Slash commands

| command                  | effect                                                                 |
|--------------------------|------------------------------------------------------------------------|
| `/help`                  | list all commands                                                      |
| `/goal <objective>`      | set a session goal that persists across turns                          |
| `/goal`                  | show current goal status                                               |
| `/goal pause`            | temporarily suspend goal — no continuation prompt injected             |
| `/goal resume`           | reactivate a paused goal                                               |
| `/goal clear`            | remove the goal                                                        |
| `/goal ... --budget=N`   | attach token budget; hitting it flips status to `budget_limited`       |
| `/model [name]`          | show/set active model                                                  |
| `/compact`               | summarize the conversation to reclaim context (also auto at ~150k tok) |
| `/cost`                  | token usage this session                                              |
| `/context`               | conversation size (messages / approx tokens)                          |
| `/todos`                 | show the current todo list                                            |
| `/plan <task>`           | research read-only, then present a plan via `ExitPlanMode`             |
| `/init`                  | analyze the repo and write a CLAUDE.md                                 |
| `/commit`                | review changes and create a git commit                                |
| `/review`                | review the current working diff                                       |
| `/doctor`                | environment + auth health                                             |
| `/clear`                 | reset conversation history                                             |
| `/dump`                  | print raw message log (debug)                                          |
| `/exit`                  | quit                                                                   |

Tools exposed to the model: `Bash`, `Read`, `Edit`, `Write`, `Glob`, `Grep`, `WebFetch`,
`WebSearch`, `Task` (synchronous sub-agent), `TodoWrite`, `ExitPlanMode`, plus the `/goal`
lifecycle tools.

## How `/goal` works

- **Storage.** Per-session `goal.json` under `~/.bankai/sessions/<id>/`.
- **Continuation prompt.** Before each user turn (while status is `active`), a hidden `<objective>...` prompt is injected. Adapted from codex's `continuation.md`. Objective is wrapped as user-provided data so it cannot override the system prompt.
- **Budget accounting.** After every model turn, `tokens_used` and `time_used_seconds` are bumped from the streaming usage. When `tokens_used >= token_budget`, status flips to `budget_limited` and a `budget_limit` prompt fires once so the model summarizes and stops.
- **Objective replacement.** Setting `/goal` while another goal is active queues an `objective_updated` prompt for the next turn.
- **Model-side lifecycle tools.** Three tools are exposed to the model:
  - `create_goal(objective, token_budget?)` — establish a goal from a user request that will span turns
  - `update_goal(status: "complete"|"blocked")` — under strict audit rules from the continuation prompt
  - `get_goal()` — read current state

## Layout

```
cmd/bankai/            binary entrypoint (+ `codex login|logout` subcommands)
internal/agent/        message + content-block types (wire types)
internal/provider/     Anthropic streaming client + OpenAI Codex Responses adapter
internal/codex/        OpenAI Codex OAuth (PKCE, port 1455, refresh, token store)
internal/tools/        Bash, Read, Edit, Write, Glob, Grep, Web*, Task, TodoWrite, plan, goal
internal/commands/     slash-command registry (/goal /compact /cost /init /commit /review ...)
internal/goal/         Goal state, persistence, prompt templates
internal/session/      per-session directory management
internal/engine/       tool-calling loop + compaction + subagent runner + usage tracking
internal/tui/          plain-stdin REPL with ANSI streaming and goal footer
internal/config/       env + data dir + provider selection
```

## Port status vs. vibelearn

Reference source lives under `_vibelearn/learnvibe` (not built, not committed — gitignored).

### Ported ✅
- Agent loop (perceive → reason → act → observe), streaming.
- Tools: Bash, Read, Edit, Write, Glob, Grep (ripgrep + Go fallback), WebFetch, WebSearch,
  Task (synchronous sub-agent), TodoWrite, ExitPlanMode.
- Providers: Anthropic (OAuth + API key), OpenAI Codex (subscription OAuth + Responses API),
  Foundry, `ANTHROPIC_BASE_URL` gateway.
- Context compaction (manual `/compact` + auto-compact threshold), token/usage tracking.
- Slash commands: `/goal /model /compact /cost /context /todos /plan /init /commit /review
  /doctor /clear /dump /exit`.
- Claude Code JSONL transcript interop (`-c` / `--resume`).

### Roadmap to full parity 🚧
Not deferred — planned work, ordered by dependency in [CLAUDE.md](./CLAUDE.md).

| # | area | vibelearn has | status here |
|---|------|---------------|-------------|
| 1 | **Permission gate** | tool permissions, sandbox toggle | none yet — tools run unrestricted; enforces plan mode too |
| 2 | **Async tasks** | background Task Create/Get/List/Stop, cron triggers | only synchronous `Task` today |
| 3 | **Real TUI** | Ink render engine, Vim mode, themes, keybindings, statusline | plain stdin REPL + ANSI |
| 4 | **Cost UI** | rate-limit / billing header display | `/cost` totals only |
| 5 | **MCP** | full client, OAuth, registry, transports | planned |
| 6 | **LSP** | diagnostics client | planned |
| 7 | **Memory** | SessionMemory, extractMemories, autoDream, memdir, team sync | planned |
| 8 | **Skills** | bundled + user skill system | planned |
| 9 | **Plugins** | install/manage, marketplace | planned |
| 10 | **Bedrock + Vertex** | AWS SigV4 / GCP ADC providers | planned (gateway works via `ANTHROPIC_BASE_URL`) |
| 11 | **Remote/coordinator** | WebSocket sessions, multi-agent, upstream proxy | planned |
| 12 | **Voice** | streaming STT, dictation | planned |
| 13 | **IDE integration** | VS Code / JetBrains bridge, diff-in-IDE | planned |
| 14 | **Full command surface** | ~120 slash commands | ~14 core commands |
| 15 | **Feature-flag build** | 88 compile-time flags | plain `make build` |
