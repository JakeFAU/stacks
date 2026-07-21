# Codex Handoff: Manager Confidence Signal PoC

## Open this checkout

Add this exact folder as a **local Codex project**:

```text
/Users/jacob/dev/personal/stacks/.worktrees/manager-confidence-poc
```

This is the active implementation checkout. Do not resume from the parent
`/Users/jacob/dev/personal/stacks` checkout; that checkout is on the design
branch and does not contain the completed implementation tasks.

Active branch:

```text
agent/manager-confidence-poc-implementation
```

## First prompt in the local Codex project

```text
Read AGENTS.md and CODEX_HANDOFF.md completely. Then read
docs/superpowers/specs/2026-07-21-manager-confidence-poc-design.md,
docs/superpowers/plans/2026-07-21-manager-confidence-poc.md, and
.superpowers/sdd/progress.md. Resume the implementation at Task 3 using
superpowers:subagent-driven-development. Use a fresh implementer and an
independent task reviewer for every task. Do not redo Tasks 1 or 2. Continue
through the final whole-branch review and verification without pausing between
tasks unless genuinely blocked. Do not push, open a PR, merge, deploy, or enable
cloud logging without my explicit request.
```

## Product goal

Build a local CLI that reads Gemini meeting artifacts from one configured
Google Drive folder, correctly handles their tabbed Google Doc format, resolves
one employee-manager pair with reviewable identity guesses, and reports cautious
changes in observable interaction signals with exact transcript citations and
counterevidence.

The PoC does **not** estimate a manager's hidden mental state as fact. It may
report a `possible declining-confidence signal` only when deterministic
admission rules and dated transcript evidence support that hypothesis.

## Authoritative documents

- Design: `docs/superpowers/specs/2026-07-21-manager-confidence-poc-design.md`
- Implementation plan: `docs/superpowers/plans/2026-07-21-manager-confidence-poc.md`
- Durable subagent ledger: `.superpowers/sdd/progress.md`
- Repository rules: `AGENTS.md`

The plan has nine tasks. Follow it in order under strict red-green-refactor.

## Completed implementation

### Task 1: command routing and typed PoC configuration

Status: complete, independently reviewed, full Go suite passed.

Commits:

```text
cb85dba Add Stacks command routing
59ff2b3 Reject blank PoC settings
```

Important behavior:

- no arguments still run the HTTP server;
- unknown commands fail;
- PoC configuration is optional for `serve` and validated per PoC command;
- whitespace-only required values fail fast;
- transcript and notes title sets are normalized and must be disjoint.

### Task 2: tab-aware source and immutable evidence

Status: complete, independently reviewed, full Go suite passed.

Commit:

```text
b9fc27f Define tabbed meeting evidence
```

Important behavior:

- notes-first/transcript-second Gemini Docs are handled correctly;
- child tabs are traversed recursively in deterministic UI order;
- exactly one configured transcript tab is required;
- paragraph, table-cell, and table-of-contents text is extracted without
  flattening tab provenance;
- document identity includes ordered tab structure and content;
- evidence spans require exact text and valid UTF-8 byte boundaries;
- citations retain provider document ID, document digest, and immutable tab ID.

Google API dependencies are already committed in `go.mod` and `go.sum`.

## Exact resume point

Resume at **Task 3: PostgreSQL schema and repositories**.

Task 3 was dispatched in the previous environment but interrupted before it
changed any tracked files. There is no partial Task 3 code to preserve or
repair. Generate a fresh Task 3 brief from the plan and dispatch a fresh
implementer.

Known local prerequisites at handoff time:

- no `.env` exists in either checkout;
- Docker was unavailable;
- therefore Task 3 migration application and PostgreSQL integration tests were
  not runnable;
- do not invent credentials or claim database verification;
- build the forward-only migration, repositories, and explicitly gated
  integration tests, then report live database verification as blocked until
  the prerequisites exist.

## Execution conventions

- The user's default is always subagent-driven execution.
- Use one fresh implementer at a time, followed by an independent task reviewer.
- Subagents in the prior sandbox could not access the default macOS Go build
  cache. Prefix their Go commands with:

```text
GOCACHE=/tmp/stacks-manager-confidence-go-cache
```

- Subagent Git commits also hung in the prior sandbox. If that recurs, let the
  subagent own implementation/tests/report and have the controller perform only
  the privileged commit after verification.
- Do not run multiple implementers in parallel; tasks touch shared contracts.
- Use the task brief, report, and review-package files under
  `.superpowers/sdd/` so context survives compaction.

## Verification state

Last confirmed after Task 2:

```text
GOCACHE=/tmp/stacks-manager-confidence-go-cache make test
```

Result: pass.

Repository-pinned Staticcheck remained unverified because the prior environment
could not resolve `proxy.golang.org`. Retry it in the local Codex project before
completion:

```text
GOCACHE=/tmp/stacks-manager-confidence-go-cache make staticcheck
```

Treat unit tests, Staticcheck, build, PostgreSQL integration, Google live auth,
Drive live ingestion, and Bedrock live invocation as separate verification
claims. If credentials are unavailable, report code/test completion without
claiming the PoC is live-validated.

## Delivery boundary

Do not push, open a pull request, merge, deploy, delete worktrees, enable Bedrock
invocation logging, or copy private transcript content into Git without explicit
user authorization.
