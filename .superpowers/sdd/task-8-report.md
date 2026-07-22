# Task 8 implementation report

## Status

GREEN. Temporal pair analysis, durable provenance/cache identity, and cited
`stacks analyze` output are implemented and unit-test complete. PostgreSQL and
Bedrock live behavior were not exercised.

## RED evidence

The implementation followed observable red-green cycles:

- Admission tests first failed because `AdmitConclusion`, typed signals, and
  report statuses did not exist.
- Service tests first failed because the pair snapshot, analysis service,
  stable identity, completion, and report contracts did not exist.
- Mention-provenance tests first failed because ingestion discarded the
  subject/object model-local mention keys.
- The forward-migration test first failed because the Task 8 migration did not
  exist.
- The PostgreSQL eligibility test first failed because
  `AnalysisRepository.LoadPairInputs` did not exist.
- CLI tests first failed because `AnalyzeCommand` did not exist.
- Lazy routing tests first failed with `unknown command "analyze"`, and command
  registration tests first failed because `analyze` was absent.
- Self-review regression tests caught and drove fixes for conflict admission,
  deterministic source-linked counterevidence, missing required model-output
  fields, unknown observation mention references, canonical UUID identity, and
  repeated analysis inputs.

## GREEN behavior

- Uses a finite category, direction, and four-status conclusion vocabulary.
- Separates source-valid time from recorded time, orders dated signals
  deterministically, and keeps unknown time in a separate report section.
- Requires two distinct dated meetings before model synthesis.
- Admits `possible declining-confidence signal` only with a cited later
  weakening signal and an earlier cited comparison from a different date.
- Preserves conflict independently of confidence and deterministically
  downgrades unsupported decline proposals.
- Sends only dated, validated, transcript-backed signals to the analysis model;
  unknown-time signals remain visible but do not establish a trend.
- Uses the reviewed `analyze-v1` prompt/schema boundary and strictly validates
  required fields, allowed conclusions, and cited signal identifiers.
- Derives completed-run identity from the canonical configured pair, ordered
  immutable inputs, prompt version, and policy version.
- Returns cached reports for identical identities and persists new identities
  when effective resolution decisions or other immutable inputs change.
- Persists the bounded report, exact citations, model ID, Bedrock region,
  maximum output tokens, prompt/policy versions, and every ordered input. Raw
  prompts and raw model output are not accepted by the persistence API.
- Renders conclusion, rationale, limitations, exact dates, chronology,
  counterevidence, unknown time, gaps, tab-specific Drive URLs, offsets, and
  exact quotes to explicit CLI stdout only.
- Emits one bounded pair-analysis span with explicit `OK` on success and one
  low-cardinality decision event per successful analysis invocation.

## Earned schema extension

`00004_temporal_pair_analysis.sql` adds nullable immutable
`subject_mention_id` and `object_mention_id` links to observations. New
ingestion completions store these links atomically. Analysis resolves each link
through the current non-superseded accepted/created resolution decision at
query time. Therefore:

- pending guesses are excluded;
- accepting a decision can admit the already-persisted signal without
  re-ingestion;
- correcting a decision changes pair eligibility and cache identity;
- observation, decision, and prior analysis history remain append-only.

The columns are nullable so the forward migration does not rewrite or invent
provenance for pre-existing rows. Such legacy rows remain ineligible for this
mention-resolved analysis path unless they already have audited links.

The migration also stores bounded analysis metadata/report JSON and replaces
the overly restrictive per-run input-digest uniqueness constraint with a
per-run `(kind, input_id)` uniqueness constraint. Distinct immutable inputs may
legitimately share a content digest.

## Files

- Added `internal/analysis/signal.go`, `signal_test.go`, `service.go`, and
  `service_test.go`.
- Added `internal/cli/analyze.go` and `analyze_test.go`.
- Added `db/migrations/00004_temporal_pair_analysis.sql`.
- Updated `internal/storage/analysis.go`, `documents.go`, `graph.go`, storage
  migration/integration tests, and ingestion completion/validation tests.
- Updated lazy command routing and executable wiring in `internal/app` and
  `cmd/stacks`.

## Verification

- `make fmt` — passed.
- `go test ./internal/analysis ./internal/cli ./internal/storage` with
  `GOCACHE=/tmp/stacks-manager-confidence-go-cache` — passed.
- `make test` with the same `GOCACHE` — passed for all packages.
- `make staticcheck` with the same `GOCACHE` — passed using the repository-pinned
  Staticcheck release. The first sandboxed attempt could not resolve the Go
  proxy; an escalated rerun found one deprecated test helper, which was fixed,
  and the final rerun passed.
- `git diff --check` — passed.

## Self-review and caveats

- No transcript text, names, email addresses, Drive URLs, prompts, raw model
  output, credentials, or live corpus data were added to logs, telemetry, or
  fixtures. All fixtures are synthetic.
- `STACKS_TEST_DATABASE_URL` was not configured, so PostgreSQL integration
  tests were compiled but skipped. The migration and effective-decision query
  have not been executed against a live PostgreSQL instance in this task.
- No Bedrock invocation was attempted because the account has no useful model
  quota. This work is test-complete, not live-validated.
- Google Drive behavior was not exercised; Task 8 reuses the already approved
  persisted document/tab/evidence boundary.
- Doctor, documentation, and live acceptance remain Task 9 scope.
