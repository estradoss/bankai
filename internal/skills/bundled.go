package skills

// Bundled skills ship with the binary (ported from vibelearn's
// src/skills/bundled). They are registered like on-disk skills but need no
// files. User/project skills of the same name override them.

// SourceBundled marks a skill that ships with bankai.
const SourceBundled Source = "bundled"

// AddBundled merges the built-in bundled skills into the set. On-disk skills
// (user/project/plugin) already loaded with the same name are NOT overwritten,
// so users can shadow a bundled skill by providing their own.
func (s *Set) AddBundled() {
	for _, sk := range bundledSkills() {
		if _, exists := s.byName[sk.Name]; exists {
			continue
		}
		sk.Source = SourceBundled
		s.byName[sk.Name] = sk
	}
}

func bundledSkills() []Skill {
	return []Skill{
		{
			Name:        "simplify",
			Description: "Review changed code for reuse, quality, and efficiency, then fix any issues found.",
			Body:        simplifyBody,
		},
		{
			Name:        "stuck",
			Description: "Diagnose a frozen, stuck, or very slow Claude Code session on this machine (diagnostic only).",
			Body:        stuckBody,
		},
		{
			Name:        "skillify",
			Description: "Capture this session's repeatable process as a reusable skill (SKILL.md), interviewing you with AskUserQuestion.",
			Body:        skillifyBody,
		},
		{
			Name:        "verify",
			Description: "Verify a code change actually does what it should by exercising it end-to-end and observing behavior.",
			Body:        verifyBody,
		},
		{
			Name:        "remember",
			Description: "Review the memory landscape (CLAUDE.md, CLAUDE.local.md, stored memories) and propose changes grouped by action type.",
			Body:        rememberBody,
		},
	}
}

const simplifyBody = `# Simplify: Code Review and Cleanup

Review all changed files for reuse, quality, and efficiency. Fix any issues found.

## Phase 1: Identify Changes

Run ` + "`git diff`" + ` (or ` + "`git diff HEAD`" + ` if there are staged changes) to see what changed. If there are no git changes, review the most recently modified files that the user mentioned or that you edited earlier in this conversation.

## Phase 2: Launch Three Review Agents in Parallel

Use the Task tool to launch all three agents concurrently in a single message. Pass each agent the full diff so it has the complete context.

### Agent 1: Code Reuse Review
1. Search for existing utilities and helpers that could replace newly written code (utility directories, shared modules, files adjacent to the changed ones).
2. Flag any new function that duplicates existing functionality; suggest the existing function instead.
3. Flag inline logic that could use an existing utility — hand-rolled string manipulation, manual path handling, custom environment checks, ad-hoc type guards.

### Agent 2: Code Quality Review
1. Redundant state that duplicates existing state or could be derived.
2. Parameter sprawl — new parameters instead of generalizing existing ones.
3. Copy-paste with slight variation that should be unified.
4. Leaky abstractions exposing internals or breaking boundaries.
5. Stringly-typed code where constants/enums/branded types already exist.
6. Unnecessary comments explaining WHAT (delete); keep only non-obvious WHY.

### Agent 3: Efficiency Review
1. Unnecessary work — redundant computation, repeated reads, duplicate API calls, N+1.
2. Missed concurrency — independent operations run sequentially.
3. Hot-path bloat added to startup or per-request/per-render paths.
4. Recurring no-op updates in loops/intervals/handlers — add change-detection guards.
5. Unnecessary existence checks (TOCTOU) — operate directly and handle the error.
6. Memory — unbounded structures, missing cleanup, listener leaks.
7. Overly broad operations — reading whole files when a portion suffices.

## Phase 3: Fix Issues

Wait for all three agents to complete. Aggregate their findings and fix each issue directly. If a finding is a false positive, note it and move on. When done, briefly summarize what was fixed (or confirm the code was already clean).`

const stuckBody = `# /stuck — diagnose frozen/slow Claude Code sessions

The user thinks another Claude Code session on this machine is frozen, stuck, or very slow. Investigate and report.

## What to look for

Scan for other Claude Code processes (exclude the current one). Process names are typically ` + "`claude`" + ` (installed) or ` + "`cli`" + ` (native dev build). Signs of a stuck session:
- High CPU (>=90%) sustained — likely an infinite loop. Sample twice, 1-2s apart, to confirm.
- Process state ` + "`D`" + ` (uninterruptible sleep) — often an I/O hang.
- Process state ` + "`T`" + ` (stopped) — probably an accidental Ctrl+Z.
- Process state ` + "`Z`" + ` (zombie) — parent isn't reaping.
- Very high RSS (>=4GB) — possible memory leak.
- Stuck child process — a hung ` + "`git`" + `, ` + "`node`" + `, or shell subprocess. Check ` + "`pgrep -lP <pid>`" + `.

## Investigation steps

1. List all Claude Code processes:
   ` + "`ps -axo pid=,pcpu=,rss=,etime=,state=,comm=,command= | grep -E '(claude|cli)' | grep -v grep`" + `
2. For anything suspicious, gather context: child processes (` + "`pgrep -lP <pid>`" + `); re-sample CPU after 1-2s; note hung child command lines; check ` + "`~/.claude/debug/<session-id>.txt`" + ` tail.
3. Optionally, a stack sample for a truly frozen process (macOS: ` + "`sample <pid> 3`" + `).

## Report

Only report if you actually found something stuck. If every session looks healthy, say so directly. Include PID, CPU%, RSS, state, uptime, command line, child processes, and your diagnosis.

## Notes
- Don't kill or signal any processes — diagnostic only.
- If the user gave an argument (a PID or symptom), focus there first.`

// skillifyBody is adapted from vibelearn's skillify skill. The dynamic
// session-memory/user-message placeholders are dropped — the model reflects on
// the live conversation instead — and it leans on bankai's AskUserQuestion tool.
var skillifyBody = "# Skillify\n\n" +
	"You are capturing this session's repeatable process as a reusable skill.\n\n" +
	"## Your Task\n\n" +
	"### Step 1: Analyze the Session\n" +
	"Reflect on this conversation and identify:\n" +
	"- What repeatable process was performed\n" +
	"- The inputs/parameters\n" +
	"- The distinct ordered steps\n" +
	"- The success artifacts/criteria for each step (e.g. not just \"writing code\" but \"an open PR with CI passing\")\n" +
	"- Where the user corrected or steered you\n" +
	"- What tools and permissions were needed\n\n" +
	"### Step 2: Interview the User\n" +
	"Use the AskUserQuestion tool for ALL questions — never ask via plain text. Iterate each round until the user is happy. The user always has a freeform \"Other\" option; do not add your own \"needs tweaking\" option.\n\n" +
	"**Round 1 — high level:** Suggest a name and description; ask to confirm or rename. Suggest goal(s) and success criteria.\n\n" +
	"**Round 2 — details:** Present the high-level steps as a numbered list. If the skill needs arguments, suggest them. Ask where to save:\n" +
	"- **This repo** (`.claude/skills/<name>/SKILL.md`) — project-specific workflows\n" +
	"- **Personal** (`~/.claude/skills/<name>/SKILL.md`) — follows you across repos\n\n" +
	"**Round 3 — per step:** For each major step, if not obvious, ask: what it produces that later steps need; what proves it succeeded; whether to confirm before proceeding (especially irreversible actions); whether any steps can run in parallel; hard constraints.\n\n" +
	"**Round 4 — final:** Confirm when the skill should be invoked and its trigger phrases; ask for any gotchas. Don't over-ask for simple processes.\n\n" +
	"### Step 3: Write the SKILL.md\n" +
	"Create the skill directory and file at the chosen location. Format:\n\n" +
	"```markdown\n" +
	"---\n" +
	"name: <skill-name>\n" +
	"description: <one-line description>\n" +
	"when_to_use: <when to auto-invoke, with trigger phrases and example messages>\n" +
	"argument-hint: \"<hint showing argument placeholders>\"\n" +
	"---\n\n" +
	"# <Skill Title>\n\n" +
	"## Inputs\n- `$arg_name`: description\n\n" +
	"## Goal\nClearly stated goal with defined completion artifacts/criteria.\n\n" +
	"## Steps\n\n### 1. Step Name\nWhat to do — specific and actionable, with commands when appropriate.\n\n" +
	"**Success criteria**: REQUIRED on every step — shows the step is done and it's safe to move on.\n" +
	"```\n\n" +
	"**Per-step annotations:** Success criteria (required); Execution (`Direct` default, `Task agent`, or `[human]`); Artifacts (data later steps need); Human checkpoint (pause before irreversible actions); Rules (hard rules, informed by user corrections).\n\n" +
	"**Frontmatter rules:** `when_to_use` is critical — start with \"Use when…\" and include trigger phrases. Only include `argument-hint` if the skill takes parameters; use `$name` in the body for substitution.\n\n" +
	"### Step 4: Confirm and Save\n" +
	"Before writing, output the complete SKILL.md as a code block for review. Then confirm with AskUserQuestion (\"Does this SKILL.md look good to save?\"). After writing, tell the user where it was saved, how to invoke it (`/<skill-name> [arguments]`), and that they can edit the SKILL.md directly to refine it."

// verifyBody reconstructs the verify skill (the vibelearn snapshot shipped a
// stub for it). Guidance: exercise the change end-to-end, don't just run tests.
const verifyBody = `# Verify a change end-to-end

Confirm a code change actually does what it should by exercising the affected
behavior and observing the result — not just by checking that tests or the
build pass.

## When to run
Before committing a nontrivial change to product source. Skip it for diffs that
only touch tests, docs, or config with no runtime surface to drive.

## Steps

### 1. Identify what changed and what it should do
Run ` + "`git diff`" + ` to see the change. State, in one sentence, the observable
behavior it is supposed to produce. **Success criteria**: you can name a concrete
input and the output it should now produce.

### 2. Pick the narrowest way to drive it
Prefer the real flow over a proxy:
- CLI change → run the command with the affected flags and read the output.
- Server/API change → start the server, hit the affected endpoint (curl), check the response.
- Library/function change → call it from a tiny script or the REPL tool with representative inputs.
- UI change → drive the actual page (browser automation) and observe the rendered result.

**Success criteria**: you have a command or interaction that triggers exactly the changed code path.

### 3. Exercise it and observe
Run the flow. Compare the observed behavior to what step 1 said it should do.
Try at least one edge case (empty input, error path, boundary value).

**Success criteria**: observed behavior matches the intended behavior, including the edge case.

### 4. Report
State plainly what you drove, what you observed, and whether it matched. If it
did not match, say so with the actual output — do not claim success on a build
or test pass alone.`

// rememberBody adapts vibelearn's remember skill to bankai's memory subsystem
// (create_memory/search_memory + MEMORY.md) alongside CLAUDE.md/CLAUDE.local.md.
const rememberBody = `# Memory Review

## Goal
Review the memory landscape and produce a report of proposed changes grouped by
action type. Do NOT apply changes — present proposals for user approval.

## Steps

### 1. Gather all memory layers
Read CLAUDE.md and CLAUDE.local.md from the project root (if present). Review the
stored memories via ` + "`search_memory`" + ` (and the MEMORY.md index already seeded into
your system prompt). Note which layers exist.
**Success criteria**: you have the contents of all layers and can compare them.

### 2. Classify each stored memory
For each substantive memory, pick the best destination:
- **CLAUDE.md** — project conventions all contributors follow ("use bun not npm", "tests: go test").
- **CLAUDE.local.md** — personal instructions for this user, not shared ("prefer concise replies", "don't auto-commit").
- **Stay a memory** — working notes, temporary context, or entries that don't clearly fit a file.

Distinctions: CLAUDE.md/CLAUDE.local.md hold instructions for the agent, not external-tool preferences (editor theme etc.). Workflow practices (PR/merge/branch conventions) are ambiguous — ask whether personal or project-wide. When unsure, ask.
**Success criteria**: each memory has a proposed destination or is flagged ambiguous.

### 3. Identify cleanup opportunities
Across layers, find: **duplicates** (a memory already in CLAUDE.md → propose deleting the memory), **outdated** entries (a file contradicted by a newer memory → propose updating the file), and **conflicts** (note which is more recent).
**Success criteria**: all cross-layer issues identified.

### 4. Present the report
Group by action type: (1) Promotions — memories to move into a file, with rationale; (2) Cleanup — duplicates/outdated/conflicts; (3) Ambiguous — needs user input; (4) No action needed.
If there are no stored memories, say so and offer to review CLAUDE.md for cleanup.
**Success criteria**: the user can approve/reject each proposal individually.

## Rules
- Present ALL proposals before making any changes.
- Do NOT modify files or delete memories without explicit user approval.
- Ask about ambiguous entries — don't guess.`
