# bankai IDE bridge (VS Code)

Connects VS Code to a running bankai agent.

## Use

1. Start the agent with the bridge: `bankai --ide` (optionally `--ide-port N`).
   It writes a lockfile at `~/.claude/ide/<port>.lock`.
2. Install this extension (dev): copy `editors/vscode/` into
   `~/.vscode/extensions/bankai-ide/`, or run it from the Extension Development
   Host (`F5`).
3. The extension auto-discovers the lockfile and connects. Your current
   selection is shared with the agent (readable via the `ide_selection` tool),
   and the agent's `ide_open` / `ide_diff` requests are applied in the editor.

Run **bankai: Reconnect to agent bridge** from the command palette if you start
the agent after VS Code.

## Protocol

Plain HTTP against the bridge (see `internal/bridge`):

- `POST /v1/selection` — push `{file, text, startLine, endLine}`
- `GET  /v1/commands`  — drain agent→IDE `{kind: "openFile"|"showDiff", file, ...}`
- `POST /v1/diagnostics` — push editor diagnostics (optional)

No npm dependencies — uses Node's built-in `http`/`fs`, bundled with VS Code.

A JetBrains plugin can implement the same three calls against the same lockfile.
