# Task D5.1 fix 1 report — mandatory PostgreSQL parity gate

## Finding and root cause

Review reproduced the current-knowledge subtest against the configured
synthetic PostgreSQL database:

```text
go test ./internal/query \
  -run '^TestPostgresRepositoryPreservesGenericTemporalEvidenceParity$/current$' \
  -count=1
```

Canonical SQL returned authority-excluded coverage for both requested
`EntityMatchAny` endpoints of the retired admission observation:

- `entity:atlas-owner`
- `entity:atlas-reviewer`

The independently assembled current oracle contained only the owner coverage,
so structural equality failed. The adapter output was correct and was not
suppressed.

The same review found that `make test-integration` executed the adapter module
and selected root packages but omitted `./internal/query`. The D5.1 root
PostgreSQL parity test could therefore appear covered by the full integration
gate without being executed.

## RED evidence

The reviewer command failed with three actual gaps versus two expected gaps;
the missing expected row was:

```text
authority-excluded entity:atlas-reviewer atlas.responsibility/admission
```

An executable Make dry-run contract test was added before changing the target.
It failed as intended:

```text
sh scripts/check-test-integration-packages.sh
test-integration does not execute ./internal/query
```

The contract also requires `./internal/analysis`, preserving the approved D5
ordering until the later removal task.

## Minimal fix

- Added exact reviewer-scoped authority coverage to the independent current
  in-memory oracle.
- Added a focused assertion that the current admission authority gaps contain
  exactly the owner and reviewer IDs.
- Added `./internal/query` to `make test-integration` without removing
  `./internal/analysis`.
- Added `scripts/check-test-integration-packages.sh` and wired it into
  `make test` as `test-integration-contract`.
- Updated the original D5.1 report so its integration evidence no longer
  implies the root query package ran before the Make correction.

No runtime query, adapter, schema, migration, provider, or private-data behavior
changed.

## GREEN evidence

Focused public PostgreSQL parity with the ignored local environment loaded
without printing values:

```text
go test ./internal/query \
  -run '^TestPostgresRepositoryPreservesGenericTemporalEvidenceParity$' \
  -count=1
ok stacks/internal/query
```

Other parity gates:

```text
go test ./internal/query \
  -run 'TestGenericTemporalQueryPreservesVerticalEvidenceParity|TestPostgresRepositoryPreservesGenericTemporalEvidenceParity' \
  -count=1
ok stacks/internal/query

go test ./internal/cli -run 'Test.*Parity' -count=1
ok stacks/internal/cli

cd adapters/postgres
go test ./... -run TestTemporalQueryPostgresProjectsGenericParityCandidates -count=1
ok github.com/JakeFAU/stacks/adapters/postgres
```

The full configured integration gate now visibly includes the root query
package:

```text
make test-integration ENV_FILE=.env
ok github.com/JakeFAU/stacks/adapters/postgres
ok stacks/internal/ingest
ok stacks/internal/directory
ok stacks/internal/analysis
ok stacks/internal/app
ok stacks/internal/doctor
ok stacks/internal/query
```

## Deterministic and migration gates

All passed:

- `make fmt`
- `sh scripts/check-test-integration-packages.sh`
- `make test`
- `make test-race`
- `make staticcheck`
- `make build`
- `make modules-check`
- `make db-status ENV_FILE=.env`
- `git diff --check`
- migration and manifest `git diff --exit-code`

Database status remained core current `3/3`; optional directory scope remained
absent and unconfigured. Migration SHA-256 values were unchanged:

```text
0b264c7fb57e31f97335e4373c988b8e7425e43afbeaaf437ddec92f3db6f4a4  core 00001_documents_evidence.sql
5a0f88834edebfa587f69d24c58a3e5f62fe8205ab8e9246aeebc88dde30c5ee  core 00002_identity_admission.sql
0a9047e408fa0621c776837546460a936c0b382e00ef32299c2a613788c90c17  core 00003_extraction_observations.sql
a06de5237eb4d36af0c8c261a31131c9514b5afe26b032e18219b0ea83f5fde5  directory 00001_directory.sql
```

## Delivery boundary

The fix is committed locally with no push or pull request. D5 specialized
runtime removal remains a later task after this corrected parity gate is
reviewed.
