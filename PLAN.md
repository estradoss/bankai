# PLAN — migrate bankai's LLM layer to `charm.land/fantasy`

> **STATUS: DONE.** `internal/provider/anthropic.go` is now a thin fantasy
> adapter (build `fanthropic` provider from `AuthSource`, convert
> `[]agent.Message`↔`fantasy.Prompt`, stream, map parts→`StreamResult`). The
> hand-rolled SSE parser + Codex/OpenAI backend are deleted (`internal/codex`,
> `internal/provider/openai.go`). Engine loop kept as-is (lowest blast radius) —
> it still calls `Client.Stream(StreamRequest)`, unchanged signature. go.mod at
> 1.26.5, `GOTOOLCHAIN=auto` in Makefile. Net −~600 LOC. Verified: `go build`/
> `go test` green + live OAuth session with Bash tool round-trip. Rate-limit
> headers preserved via a capturing `http.RoundTripper` on the fantasy client.

Goal: make bankai **leaner** by delegating the LLM transport + tool-use loop to
fantasy (Charm's LLM/agent library, the engine behind crush), while keeping
bankai's differentiators — `~/.claude` jsonl interop, `/goal` engine, auth —
100% in-house. Anthropic-only for now; provider breadth (OpenAI, local models)
becomes near-free config later.

## Decision & rationale

- Delete the parts fantasy does better/generically: the hand-rolled Anthropic
  SSE parser (`internal/provider/anthropic.go`) and the stream→dispatch→repeat
  loop (`internal/engine`). ~1–1.5k lines of our code gone.
- **Drop Codex/OpenAI** (`internal/codex`, `internal/provider/openai.go`) — not
  accommodating non-Anthropic right now. Removes the one migration risk.
- Trade: our code gets thinner; dep tree gets fatter (fantasy + anthropic-sdk-go)
  and build needs **go 1.26** — acceptable, `GOTOOLCHAIN=auto` handles it.
- Ancillary wins even Anthropic-only: fantasy owns retry/backoff (honors
  `retry-after`), 401 `OnAuthRefresh`, `jsonrepair` on malformed tool JSON.

## Pilot (DONE — de-risks the migration)

`tmp/pilot/` proved `~/.claude` OAuth works through fantasy's anthropic provider:
`anthropic.New(WithSkipAuth(true), WithHeaders{Authorization: "Bearer <tok>",
"anthropic-beta": "oauth-2025-04-20"})` → streamed reply. Findings:
- OAuth Bearer + beta-header path supported (no x-api-key). ✅
- **go 1.26 is a non-issue** — `GOTOOLCHAIN=auto` auto-downloads + builds. ✅
- Subscription OAuth requires the `"You are Claude Code…"` system prompt as the
  first block — bankai already injects `ClaudeCodePrefix`. ✅

## What stays vs goes

| keep (untouched) | replace / delete |
|---|---|
| `internal/transcript` (jsonl read/write) | `internal/provider/anthropic.go` SSE → fantasy `anthropic` provider |
| `internal/auth` (token resolve/refresh) | `internal/engine` loop → fantasy `Agent` (or keep loop, call `model.Stream`) |
| `/goal` (`internal/goal`) | `internal/codex` + `internal/provider/openai.go` → **delete** |
| tools' logic + JSON schemas | Codex CLI subcommand + `CLAUDE_CODE_USE_OPENAI` routing → delete |
| TUI, commands, memory, mcp, lsp, skills, plugins | |

## Phases

### Phase 0 — spike the loop (before cutover)
- [ ] Extend `tmp/pilot`: build a `fantasy.NewAgent(model, WithTools(oneTool))` and
      run `agent.Stream(...)` with ONE bankai-style tool (raw `FunctionTool`
      schema) → prove tool_use dispatch + result feedback round-trips.
- [ ] Wire `OnAuthRefresh` → `internal/auth.Provider.Refresh()`; force a 401
      (expired token) and confirm auto-refresh-and-retry.

### Phase 1 — provider swap (Anthropic-only)
- [ ] New `internal/llm` (or fold into `internal/provider`): construct the
      fantasy anthropic provider from `internal/auth` (Bearer + beta header via
      `WithSkipAuth`+`WithHeaders`, token from `auth.Provider.AccessToken`).
- [ ] Expose a `LanguageModel` bankai can call; keep `provider.Client.Model`,
      `Limits`, usage accounting mapped from fantasy `Usage`/`ProviderMetadata`.
- [ ] Delete `internal/codex`, `internal/provider/openai.go`, Codex routing/flags.

### Phase 2 — loop cutover
- [ ] Replace `engine.Submit`'s hand-rolled loop with `fantasy.Agent`:
      map bankai tools → `[]fantasy.AgentTool` (wrap existing `tools.Tool`,
      reuse each `InputSchema()` as the `FunctionTool` schema).
- [ ] Map fantasy callbacks → bankai's existing hooks: `OnTextDelta`→`OnText`,
      `OnToolCall`/`OnToolResult`→`OnToolStart`, `OnStepFinish`→usage/turns.
- [ ] Preserve: permission gate (dispatch tools through `Perms`), goal
      continuation, LSP feedback, hooks, memory injection, compaction.
- [ ] Keep transcript writing exactly where it is (append each turn's messages).

### Phase 3 — verify parity
- [ ] `go build ./...` on go 1.26; `go test ./...` green.
- [ ] Drive a real session (TUI + line REPL): multi-tool turn, permission
      prompt, `/goal`, interrupt (ctrl+c → cancel turn), resume from jsonl.
- [ ] Confirm `~/.claude` jsonl still hands off to real `claude --resume`.
- [ ] Line-count before/after (target: net reduction in `internal/`).

## Non-goals
- Do NOT move jsonl/auth/goal into fantasy — they stay bankai's.
- Do NOT re-add Codex/OpenAI now (later, via a second fantasy provider = config).
- Do NOT fork fantasy — normal dependency.

## Revisit triggers
- If Codex/ChatGPT-subscription support is needed again, check whether fantasy's
  openai provider covers the Responses API + ChatGPT-subscription backend before
  re-adding.

## Reference
- Pilot: `tmp/pilot/main.go` (working OAuth-through-fantasy proof).
- Fantasy source (for API): fetched to the module cache; crush fork at
  `../crush` uses fantasy in anger (see its `internal/agent`).
- Architecture notes on the split: this repo's git history + the crush fork's
  `CLAUDE.md`.
