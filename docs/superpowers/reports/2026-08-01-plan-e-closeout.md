# Plan E Natural-Language Temporal Query Planner Closeout

Date: 2026-08-01

Plan E is closed as a planner-only increment. It adds `query ask`, which turns
one bounded private question into an existing closed temporal query request and
then executes that request through unchanged deterministic cited retrieval.
Model output remains untrusted structured input. Canonical identity, evidence,
chronology, conflict handling, limits, and citation association remain outside
the model's authority.

## Delivered scope

- The implementation was published in [PR #33](https://github.com/JakeFAU/stacks/pull/33).
- Follow-up planner performance, hardening, prompt-version, and ownership work
  was published in [PR #34](https://github.com/JakeFAU/stacks/pull/34),
  [PR #35](https://github.com/JakeFAU/stacks/pull/35),
  [PR #36](https://github.com/JakeFAU/stacks/pull/36), and
  [PR #38](https://github.com/JakeFAU/stacks/pull/38).
- Required GitHub Test and GitGuardian checks succeeded for each of those pull
  requests before merge.
- Daily-cleaner [PR #37](https://github.com/JakeFAU/stacks/pull/37) and
  [PR #39](https://github.com/JakeFAU/stacks/pull/39) were reviewed, checked,
  and merged during final reconciliation.

The final whole-tree implementation review reported no unresolved findings.
The recorded local acceptance sequence passed formatting, unit, race, static
analysis, build, module, diff, migration, and PostgreSQL integration gates.
Core migrations were current at 3/3; the optional directory migration scope was
absent and unconfigured. No migration bytes or fingerprints changed for Plan E.

## Bounded live-provider acceptance

After separate authorization, one temporary acceptance harness used:

- OpenAI `gpt-5.6-terra`;
- at most 512 output tokens and two same-provider attempts;
- an isolated PostgreSQL database containing only synthetic
  Alice/Bob/Charlie/David/Eve fixtures; and
- the reviewed `query ask` boundary followed by deterministic retrieval.

The run passed with no standard-error output. It validated structured planner
compatibility, audited plan metadata, deterministic PostgreSQL execution, and
preservation of synthetic evidence associations for that provider, model, and
fixture only. The temporary harness and database were removed afterward, the
canonical database remained empty, focused planner tests passed, and the
worktree remained clean.

No credential, private question, prompt body, raw model output, source text,
canonical ID, citation, database URL, or private corpus content is recorded in
this report.

## Explicitly unvalidated boundaries

Plan E does not claim:

- private-corpus acceptance;
- compatibility with another provider or model;
- general provider availability or model quality;
- a deliberately induced live provider refusal, incomplete response, or 429;
- entity matching or identity classification;
- narration or answer generation;
- web UX; or
- planner persistence or schema changes.

Provider refusal, incomplete-output, cancellation, and bounded same-provider
retry behavior are covered by deterministic adapter tests. A future live run
for one of those conditions remains a separately authorized provider-specific
validation action, not unfinished planner implementation.

Identity matching, narration, and web UX require new written designs and their
own implementation, publication, and validation approval gates.
