# Task D5.2 report — decouple canonical interaction mapping

## Baseline and scope

- Baseline: clean `codex/plan-d-temporal-query-design` at
  `607ae2a79448b78e6be7d20b8fe0abc4d0659503`.
- Baseline `make test`: passed before edits.
- Scope: the finite extract-v2 category/direction to canonical-predicate codec,
  its exhaustive contract tests, ingestion consumption, and the synthetic
  Drive chronology consumer.
- The existing analysis package, report statuses, conclusion admission,
  narration, pair roles, scoring, schema, prompt, configuration, migrations,
  and persistence behavior are unchanged. Later D5 tasks own their removal.

## Exact durable predicate-byte evidence

Before editing, the 20 accepted predicate strings were extracted from
`607ae2a:internal/analysis/observation_test.go`. After editing, the 20 literal
oracles were extracted from the extraction-owned round-trip test. Both sorted,
newline-delimited files contain exactly 20 lines and have the same SHA-256:

```text
bcdc70eda2a7edc46d730ec87b6619a531b1b88439f2d7102dad3b606f1168c2
```

`cmp` returned zero. The exact preserved strings are:

```text
stacks.interaction.v1/delegation_autonomy/mixed
stacks.interaction.v1/delegation_autonomy/strengthening
stacks.interaction.v1/delegation_autonomy/unclear
stacks.interaction.v1/delegation_autonomy/weakening
stacks.interaction.v1/endorsement_trust/mixed
stacks.interaction.v1/endorsement_trust/strengthening
stacks.interaction.v1/endorsement_trust/unclear
stacks.interaction.v1/endorsement_trust/weakening
stacks.interaction.v1/future_responsibility/mixed
stacks.interaction.v1/future_responsibility/strengthening
stacks.interaction.v1/future_responsibility/unclear
stacks.interaction.v1/future_responsibility/weakening
stacks.interaction.v1/scrutiny_correction/mixed
stacks.interaction.v1/scrutiny_correction/strengthening
stacks.interaction.v1/scrutiny_correction/unclear
stacks.interaction.v1/scrutiny_correction/weakening
stacks.interaction.v1/support_advocacy/mixed
stacks.interaction.v1/support_advocacy/strengthening
stacks.interaction.v1/support_advocacy/unclear
stacks.interaction.v1/support_advocacy/weakening
```

Construction and parsing also preserve the prior bounded rejection behavior:

```text
interaction observation category is invalid
interaction observation direction is invalid
interaction observation predicate namespace is invalid
interaction observation predicate category is invalid
interaction observation predicate direction is invalid
```

The extraction-owned tests cover all five extract-v2 categories crossed with
all four directions, unknown construction values, a non-versioned predicate,
the wrong namespace, the wrong version, unknown parsed category and direction,
and an extra path component.

## TDD RED

Tests were added before production code. The import-boundary test first failed
independently:

```text
go test ./internal/ingest \
  -run '^TestIngestionBuildsCanonicalInteractionObservationsWithoutAnalysisImport$' \
  -count=1
--- FAIL: TestIngestionBuildsCanonicalInteractionObservationsWithoutAnalysisImport
ingestion production import = "stacks/internal/analysis", want extraction-owned mapping boundary
```

The mandated combined RED then showed both missing extraction APIs and the
existing ingestion dependency:

```text
go test ./internal/extract ./internal/ingest \
  -run 'TestInteractionObservationPredicate|TestIngestionBuildsCanonicalInteraction' \
  -count=1
internal/extract/observation_test.go: undefined: InteractionObservationPredicate
internal/extract/observation_test.go: undefined: ParseInteractionObservationPredicate
ingestion production import = "stacks/internal/analysis", want extraction-owned mapping boundary
FAIL
```

## Minimal implementation and GREEN

`internal/extract/observation.go` now owns the versioned extract-v2 mapping and
reuses the extraction validators that already define the schema vocabulary.
Ingestion passes validated extraction strings directly to that codec. The
synthetic Drive chronology test parses through extract and asserts canonical
source-title instants or unknown time directly; it no longer constructs
analysis signals or calls manager conclusion policy.

The mandated GREEN passed:

```text
go test ./internal/extract ./internal/ingest \
  -run 'TestInteractionObservationPredicate|TestIngestionBuildsCanonicalInteraction' \
  -count=1
ok stacks/internal/extract
ok stacks/internal/ingest
```

Focused package acceptance passed:

```text
go test ./internal/extract ./internal/ingest ./internal/source/drive -count=1
ok stacks/internal/extract
ok stacks/internal/ingest
ok stacks/internal/source/drive
```

## Import evidence

The mandated raw scan returned no matches:

```text
! rg -n 'stacks/internal/analysis' internal/ingest internal/source/drive
```

The executable ingestion boundary test asks `go list` for the package's
compiled direct imports and rejects the analysis import path. A separate
compiler-derived inspection confirmed:

- `stacks/internal/ingest` directly imports `stacks/internal/extract`;
- `stacks/internal/ingest` does not directly import analysis; and
- production `stacks/internal/source/drive` imports neither analysis nor
  extraction policy.

## Deterministic gates

All completed successfully:

- `make fmt`
- `make test`
- `make test-race`
- `make staticcheck`
- `make build`
- `make modules-check`
- `git diff --check`

No provider, network, PostgreSQL, migration, secret, private-corpus, or live
source acceptance was run or required for this pure boundary move. No migration
or manifest file changed.

## Delivery boundary

The change is committed locally as `Decouple canonical interaction mapping`.
It is not pushed and no pull request is created. Two independent
post-commit specification and quality reviews remain the controller's gate.
