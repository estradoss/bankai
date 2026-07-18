# bankai (Go)

Terminal coding agent. Go rewrite of the TypeScript bankai. Ships a persistent `/goal` command inspired by [codex](https://github.com/openai/codex) and [pi-goal](https://github.com/code-yeongyu/pi-goal).

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
- `BANKAI_MODEL` — model id (default `claude-sonnet-4-6`)

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
| `/clear`                 | reset conversation history                                             |
| `/dump`                  | print raw message log (debug)                                          |
| `/exit`                  | quit                                                                   |

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
cmd/bankai/            binary entrypoint
internal/agent/        message + content-block types (wire types)
internal/provider/     Anthropic Messages API streaming client
internal/tools/        Tool interface + Bash, Read, Edit, Write, goal tools
internal/commands/     slash-command registry + /help /clear /exit /model /goal
internal/goal/         Goal state, persistence, prompt templates
internal/session/      per-session directory management
internal/engine/       tool-calling loop (Submit -> stream -> dispatch -> repeat)
internal/tui/          plain-stdin REPL with ANSI streaming and goal footer
internal/config/       env + data dir
```

## Roadmap

- Bubbletea/lipgloss TUI (spinner, tool-call panels, streaming markdown)
- More providers (OpenAI, Bedrock, Vertex, local)
- Permission gate on tool calls (currently unrestricted)
- MCP support
- Persist conversation transcript per session
- Port additional bankai commands (/init, /commit, /review, /skills)
