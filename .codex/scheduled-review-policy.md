# Scheduled Review Policy

This policy governs every named Codex scheduled-review agent in this repository.
The role prompt defines what to examine; this file defines how every review must
operate.

## Instruction Order

Before doing any work:

1. Read every applicable `AGENTS.md`, from the repository root through the
   directories being inspected or changed.
2. Read relevant repository documentation, contributor guidance, and local
   conventions.
3. Read the complete role prompt and its named journal.

Repository-specific instructions take precedence over generic examples in the
role prompt. If instructions conflict in a way that affects correctness,
ownership, safety, validation, or delivery, stop and report the conflict rather
than guessing.

## Repository Orientation

Before selecting a candidate:

- Inspect the project structure, ownership boundaries, public contracts,
  configuration, build constraints, tests, and relevant recent history when
  locally available.
- Determine the repository's actual formatting, linting, static-analysis,
  type-checking, test, and build commands. Do not assume illustrative commands
  apply.
- Inspect the working tree and preserve all unrelated user changes.
- Identify generated files and edit their source rather than generated output
  unless repository guidance explicitly requires otherwise.
- Use existing repository tools, dependencies, abstractions, and conventions.

## Selection Standard

- Select at most one logical change.
- Require concrete, repository-specific evidence that the candidate matches the
  role's objective.
- Prefer the strongest evidence, narrowest complete change, and lowest
  reasonable regression risk.
- Ambiguity about behavior, usage, ownership, product intent, rollout state,
  public contracts, or compatibility means no change.
- No change is a completely valid and successful result. Never manufacture work
  to satisfy the schedule.

## Change Discipline

When a candidate qualifies:

- Make the smallest coherent change that fully addresses it.
- Preserve public behavior and interfaces except where the role explicitly
  exists to reject established invalid or unsafe behavior.
- Preserve error, retry, fallback, transaction, security, observability, and
  compatibility semantics unless the role explicitly and safely targets one of
  them.
- Avoid unrelated cleanup, reformatting, refactoring, file moves, abstraction,
  and terminology sweeps.
- Do not add or upgrade dependencies.
- Do not modify dependency manifests, lockfiles, compiler configuration, build
  configuration, deployment configuration, schemas, migrations, or
  infrastructure unless the role prompt explicitly permits the exact change.
- Add or update the narrowest regression test needed to prove the changed
  behavior. Test-only roles follow their more specific limits.
- Never overwrite, revert, or absorb unrelated user work.

## Validation

Run focused checks first, followed by every broader check required by applicable
repository instructions. Depending on the repository, this may include
formatting, linting, static analysis, type checking, compilation, package tests,
broader tests, race detection, documentation checks, security scans, benchmarks,
or a production build.

Never claim a command passed unless it ran and passed. Never conceal, bypass,
weaken, skip, retry away, or relabel a failure to make the run appear
successful.

When a check fails:

- Investigate enough to distinguish a regression from a pre-existing or
  environment-related failure.
- Report the exact command and relevant failure honestly.
- Do not describe validation as complete when required checks did not pass.
- Revert the proposed change if its claim is unsupported or it introduces a
  regression.

Before delivery, inspect the final diff and confirm:

- Only the intended logical change is present.
- Tests actually protect the claimed behavior.
- No secret, credential, personal data, raw sensitive content, or unsafe
  diagnostic output was introduced.
- No generated file, dependency file, configuration, or unrelated code changed
  unexpectedly.
- The diff is focused and reviewable.

## Persistent Journals

Each role owns `.codex/<name>.md`.

The journal is not a work log. Record only a validated, durable,
repository-specific lesson that should materially improve future runs of that
same role. Do not record routine activity, generic advice, ordinary successful
changes, or summaries of completed work.

Use this common format:

```markdown
## YYYY-MM-DD - [Title]

**Learning:** [Durable repository-specific insight]

**Action:** [How future runs of this agent should apply it]
```

Keep entries concise. The role prompt defines which findings qualify.

A journal update may accompany a qualifying implementation pull request. A
no-change run must not modify or create the journal, commit journal changes, or
open a journal-only pull request. If the journal is absent, treat it as empty
while evaluating candidates; create it only when a qualifying pull request also
contains a validated durable learning.

## Pull Requests

Create a pull request only when:

- One qualifying change was implemented.
- The role-specific evidence supports the claim.
- Relevant validation passed, apart from clearly documented pre-existing or
  environment-related failures that do not undermine the change.
- Required regression coverage was added or existing coverage clearly proves
  the result.
- The final diff is focused and reviewable.

Reference a related issue only when one actually exists. Do not create an issue
merely to populate the pull request.

Use the role's required title and body. Every pull request must explain:

```markdown
## What

[Describe the one focused change.]

## Why

[Explain the verified problem and why it qualifies for this role.]

## Evidence

[Provide the role-specific baseline, reproduction, invariant, reference search,
contract, or other proof.]

## Validation

- `[exact command]` — passed
- `[exact command]` — passed

[List pre-existing or environment-related failures honestly.]

## Compatibility and Risk

[Explain preserved behavior, relevant edge cases, and remaining risk.]

## Rollback

[Explain how to revert or disable the change.]
```

The role prompt may replace or extend these headings when its evidence is better
expressed differently.

## No-Change Outcome

When no candidate qualifies:

- Do not edit any file.
- Do not commit.
- Do not create a pull request.
- Do not make a journal-only change.
- Report what was examined, the strongest candidates considered, and why each
  failed the role's evidence, safety, scope, or compatibility threshold.
- Report commands that actually ran and their outcomes.
- State remaining uncertainty without presenting speculation as a finding.

## Final Report

Every run ends with a concise report containing:

- Result: qualifying change implemented, or no change.
- Scope examined.
- Selected candidate and evidence, or strongest rejected candidates.
- Files changed, or none.
- Validation commands and exact outcomes.
- Pull request link, when one was created.
- Material uncertainty or follow-up that requires human judgment.

## Decision Rule

The schedule creates an opportunity to inspect the repository, not an obligation
to alter it. Spend repository complexity only when the role-specific evidence
shows that the expected benefit exceeds the maintenance cost and risk.
