# Task D5.3 report — remove pair-analysis prompt and configuration

## Baseline and staging decision

- Baseline: clean `codex/plan-d-temporal-query-design` at
  `37440fc950519eb33cc178505330e11bb696929d`.
- Pre-change `make test`, `make staticcheck`, and `git diff --check` passed.
- D5.3 could not form an honest compile- and test-safe intermediate commit:
  `internal/analysis.Service` consumed `extract.PromptContract` for its
  model-backed behavior, while `cmd/stacks` consumed the specialized pair
  configuration. Removing `analyze-v1` and `ManagerConfidenceSettings` alone
  would either break the retained package or require hidden prompt/config
  compatibility.
- The controller therefore approved one atomic D5.3+D5.4 removal. No stub,
  alias, renamed manager policy, retired environment read, or replacement
  prompt was introduced.
- Removing the strict `analysis` document key necessarily made the checked-in
  YAML and JSON examples invalid, so those two examples were updated in this
  atomic commit. `.env.example`, README, AGENTS, and the final current-document
  audit remain D5.5 work.

## Extract-v2 byte preservation

Before production deletion, `TestExtractV2SchemaAndPromptRemainByteStable`
captured independently calculated SHA-256 literals:

```text
prompt  88e19f093fb72f5b948caafd8bdc57d52e95273c5e84e465445157d05c84aedc
schema  26d5a016ac6d529b51685da2927d5fe1368fbd0dd270f05ed14a9707a427b6ea
```

The prompt file digest was rechecked after the implementation and remained
exactly:

```text
88e19f093fb72f5b948caafd8bdc57d52e95273c5e84e465445157d05c84aedc  internal/extract/prompts/extract-v2.txt
```

The byte-stability test and the complete extraction suite pass after removal.
The provider-neutral `extract.Model`, request, response, and reviewed
`extract-v2` contract remain in place.

## TDD RED

The mandated combined focused test was run before production edits:

```text
go test ./internal/config ./internal/extract ./internal/bedrock \
  -run 'Test(ConfigDocumentRejectsRetired|LoadDoesNotBindRetired|PromptContractSupportsExtractionOnly|ExtractV2Schema)' \
  -count=1
```

It failed for the intended reasons:

- strict YAML and JSON validation both accepted the retired `analysis` object;
- the environment binding table still contained the retired analysis prompt
  environment input; and
- `PromptContract` still accepted the retired pair-analysis version.

The extract-v2 byte-stability test passed during this RED run. Bedrock had no
selected failing test.

## Minimal removal and retired-input proof

- Deleted the embedded pair-analysis prompt, schema name, schema bytes, prompt
  version, schema accessor, and Bedrock pair transport case.
- `PromptContract` now supports extraction only.
- Removed the analysis document node, loader key/default/binding, application
  field/type, retired environment constants/reads, pair IDs, and
  analyze-specific model/disclosure validation.
- Strict YAML and JSON tests now reject the retired `analysis` object without
  disclosing its value.
- `TestLoadDoesNotBindRetiredAnalysisEnvironmentInputs` inspects the binding
  metadata, loads a baseline, sets all three retired names to synthetic values,
  requires a successful second load, and compares the complete `Settings`
  values for equality. This proves the names are neither bound nor read and
  cannot change settings or loading errors.
- Sync retains its extraction-version, provider-selection, credential,
  disclosure-policy, retry, and model-bound validation.

## Focused GREEN and scans

All passed:

```text
go test ./internal/config ./internal/extract ./internal/bedrock -count=1
go test ./internal/cli ./internal/app ./cmd/stacks -count=1
sh scripts/check-test-integration-packages.sh
```

The required runtime scan returned no matches:

```text
! rg -n 'AnalysisPromptVersion|AnalysisSchemaName|AnalysisJSONSchema|STACKS_ANALYSIS_PROMPT_VERSION|STACKS_EMPLOYEE_ENTITY_ID|STACKS_MANAGER_ENTITY_ID|pair_analysis|analyze-v1' internal
```

## Complete verification

All completed successfully:

- `make fmt`
- `make test`
- `make test-race`
- `make staticcheck`
- `make build`
- `make modules-check`
- `make test-integration ENV_FILE=.env`
- `make db-status ENV_FILE=.env`
- `git diff --check`
- migration byte diff against `37440fc`

The PostgreSQL gate used synthetic local fixtures only. It does not establish
document-source, directory-provider, model-provider, cloud, or private-corpus
acceptance. Database status was core current `3/3`; the optional directory
scope was absent and unconfigured. No migration byte changed.

## Delivery boundary

The atomic D5.3+D5.4 change is committed locally with subject
`Remove specialized manager analysis`. It is not pushed and no pull request is
created. Independent reviews remain the controller's gate.
