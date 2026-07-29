# Task D5.4 report — remove specialized manager analysis

## Scope and atomic dependency

D5.4 was executed atomically with D5.3 after the current dependency graph
proved that deleting the pair-analysis prompt/config alone would leave the
retained analysis service nonfunctional. The combined deletion removes the
entire specialized path rather than preserving a hidden contract, compatibility
alias, failing stub, empty package, or renamed manager policy.

The pre-change surface scan recorded:

- the `analyze` Cobra leaf and offline config target;
- app validation/bootstrap routing;
- process command-map, repository, model/disclosure, telemetry, and service
  composition;
- `internal/cli.AnalyzeCommand`;
- every file in `internal/analysis`; and
- the Make target and integration-package entry.

## TDD RED

Tests were changed before runtime deletion:

- `TestRunnerRejectsRetiredAnalyzeBeforeExecution` failed because the leaf
  still reached the execution callback;
- `TestRunnerHelpOmitsRetiredAnalyze` failed because root help still listed
  `analyze`;
- `TestExecuteRejectsRetiredAnalyzeBeforeLoadOrBootstrap` failed because the
  application still loaded settings for the command; and
- `scripts/check-test-integration-packages.sh` failed because
  `make test-integration` still executed `./internal/analysis`.

These failures demonstrated the public surface, no-bootstrap boundary, and
integration-gate change independently.

## Runtime deletion

- Deleted every production and test file under `internal/analysis`.
- Deleted `internal/cli/analyze.go` and its tests.
- Removed `CommandAnalyze`, the CLI leaf, config-validation leaf/target, app
  target, process command-map entry, canonical analysis repository, model and
  disclosure construction, service helper, and specialized tests.
- Removed the Make target and changed the executable integration-package
  contract to reject `./internal/analysis` while continuing to require
  `./internal/query`.
- Retired invocations are now rejected by the ordinary Cobra syntax boundary
  before settings loading, bootstrap, provider construction, repository
  construction, or shutdown ownership begins.

No specialized renderer, model narration, pair roles, conclusion policy,
signal policy, compatibility wrapper, or empty package remains.

## Preserved boundaries

- Sync still owns its model-provider and restricted-disclosure path.
- The provider-neutral extraction model boundary and exact `extract-v2` bytes
  are unchanged.
- All four generic query leaves remain registered.
- Query composition remains lazy and provider-free.
- App shutdown still runs exactly for successfully bootstrapped executable
  commands; syntax rejection does not acquire shutdown ownership.
- `make test-integration` still executes `internal/query`.

## Focused GREEN and scans

All passed:

```text
go test ./internal/cli ./internal/app ./cmd/stacks -count=1
sh scripts/check-test-integration-packages.sh
```

The required deletion scan returned no matches:

```text
! rg -n 'CommandAnalyze|stacks/internal/analysis|AnalyzeCommand|analysis\.Repository|^analyze:' internal cmd Makefile
```

The broader recorded surface scan also has no remaining runtime match for
`CommandAnalyze`, `internal/analysis`, `AnalyzeCommand`,
`analysis.Repository`, `make analyze`, or a Make `analyze:` target.

## Complete verification

All completed successfully:

- `make fmt`
- focused config/extract/Bedrock and CLI/app/process suites
- `make test`
- `make test-race`
- `make staticcheck`
- `make build`
- `make modules-check`
- `make test-integration ENV_FILE=.env`
- `make db-status ENV_FILE=.env`
- `git diff --check`
- migration byte diff against `37440fc`

The configured synthetic PostgreSQL integration run passed for the adapter,
ingestion, directory, app, doctor, and generic query packages. No provider,
private source, or private-corpus call was made. No migration changed.

## Delivery boundary

The atomic D5.3+D5.4 change is committed locally with subject
`Remove specialized manager analysis`. It is not pushed and no pull request is
created. Independent reviews remain the controller's gate.
