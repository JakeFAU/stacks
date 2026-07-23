# Personal Model Providers and Restricted Disclosure Design

**Date:** 2026-07-22
**Status:** Approved for implementation planning
**Scope:** Add direct OpenAI and Anthropic structured-generation adapters for personal development while making the future company-IP Bedrock boundary fail closed.

## Context

The manager-confidence proof of concept currently reads one explicitly configured Google Drive folder and sends transcript-backed extraction and analysis requests through Amazon Bedrock. The domain boundary is already provider-neutral: `internal/extract.Model` accepts a reviewed prompt/schema contract and returns untrusted JSON plus bounded metadata. Bedrock is an adapter at the process edge.

The local AWS account has no applicable Bedrock invocation quota, so the merged proof of concept cannot complete live model acceptance there. Personal OpenAI and Anthropic credentials are available for development, and a personal Drive folder may hold synthetic test documents. Future company material must still use an approved Bedrock boundary and must not depend on an operator remembering which provider is safe.

## Goals

- Allow `sync` and `analyze` to use Bedrock, OpenAI, or Anthropic through the existing `extract.Model` contract.
- Enforce an explicit personal-versus-restricted disclosure policy before private source content is read or sent.
- Keep prompts, schemas, deterministic validation, provenance, retries, and admission rules provider-neutral.
- Preserve existing Bedrock derivation and analysis digest compatibility.
- Record enough provider identity and disclosure-policy provenance to audit every new model-derived run.
- Keep credentials and private payloads out of Git, logs, metrics, traces, and errors.
- Validate direct-provider behavior with synthetic content and explicit paid live acceptance only when requested.

## Non-goals

- A plugin registry, dynamic provider loading, or generic model marketplace.
- Provider fallback, automatic model routing, or retrying one provider through another.
- Prompt changes, schema changes, confidence-policy changes, or new report conclusions.
- Embeddings, retrieval, model evaluation infrastructure, or provider price optimization.
- Expanding normal Google ingestion to write access.
- Declaring personal-provider acceptance sufficient for future company-IP acceptance.

## Chosen approach

Keep `internal/extract.Model` unchanged and add two narrow edge adapters:

- `internal/openai` uses the official OpenAI Go SDK and the Responses API.
- `internal/anthropic` uses the official Anthropic Go SDK and the Messages API.
- `internal/bedrock` continues to implement the same interface.

Provider selection belongs in the composition root. A small typed constructor receives already validated model settings and returns an `extract.Model`; it is a switch over three known providers, not a registry or factory framework. Provider SDK types remain inside their adapter packages.

Each adapter must verify and submit the exact embedded `extract.PromptContract`, use native JSON-schema constrained output, reject refusals and incomplete responses, return only valid JSON candidates to the caller, and translate errors into the existing provider-neutral authentication and authorization sentinels plus bounded provider outcomes. Native constrained decoding does not make model claims trusted: deterministic schema decoding, citation validation, admission, and persistence remain unchanged above the adapter.

Adapters may not silently remove semantic schema constraints to accommodate a provider or model. Contract tests submit the existing schemas unchanged. A model that rejects the reviewed schema is unsupported until a separately reviewed contract revision can preserve the same deterministic guarantees. This is important because structured-output APIs support documented JSON Schema subsets and may reject unsupported keywords rather than degrade gracefully.

The official API contracts informing the adapters are:

- OpenAI Responses Structured Outputs: <https://developers.openai.com/api/docs/guides/structured-outputs>
- OpenAI Responses data controls: <https://developers.openai.com/api/docs/guides/your-data#v1responses>
- Anthropic Messages structured outputs: <https://platform.claude.com/docs/en/build-with-claude/structured-outputs>

## Trust modes

Add an explicit `STACKS_DATA_MODE` with exactly two runtime values:

- `personal`: Bedrock, OpenAI, and Anthropic are permitted.
- `restricted`: only Bedrock is permitted, and Bedrock model invocation logging must be confirmed disabled before private source content is read.

Commands that can disclose model input (`sync` and `analyze`) require an explicit data mode and provider. There is no default for either. `serve`, entity inspection, and review commands retain their current minimal configuration requirements because they do not invoke a model.

Static validation runs before constructing Google, PostgreSQL, AWS runtime, OpenAI, or Anthropic clients. `restricted` with any provider other than Bedrock is an immediate configuration error.

For `restricted` Bedrock operations, a read-only AWS control-plane preflight checks invocation-logging configuration before Google authorization or document discovery. `enabled`, `unknown`, access denied, timeout, or inspection failure all stop the command. Only a confirmed `disabled` result permits source access. This is intentionally fail closed; the deployment must grant the narrow inspection permission required to prove the state.

`doctor` never invokes a model. It reports the selected data mode, provider, credential/configuration readiness, model metadata availability when the provider offers a non-invoking endpoint, and the Bedrock logging state. A paid runtime smoke remains a separate, explicitly invoked acceptance action.

## Configuration

Normalize common model policy in `internal/config`:

- `STACKS_DATA_MODE=personal|restricted`
- `STACKS_MODEL_PROVIDER=bedrock|openai|anthropic`
- `STACKS_MODEL_ID` with no default
- `STACKS_MODEL_MAX_OUTPUT_TOKENS`
- `STACKS_MODEL_MAX_ATTEMPTS`

Provider-only configuration remains provider-specific:

- Bedrock: `STACKS_AWS_PROFILE` and `STACKS_AWS_REGION`
- OpenAI: `OPENAI_API_KEY`
- Anthropic: `ANTHROPIC_API_KEY`

The existing Bedrock-specific model ID, token, and attempt variables are replaced by the normalized names rather than retained as ambiguous aliases. The implementation updates the ignored local `.env` without displaying values and keeps `.env.example` limited to empty values or safe placeholders. If both old and new names are temporarily present during the local transition, validation reports the old names as unsupported instead of silently choosing one.

Model IDs remain explicit because availability, structured-output support, latency, and price change independently. No provider or model is guessed. Provider adapters must not accept custom base URLs in this slice; that would create an unreviewed fourth disclosure boundary.

## Provider request policy

All providers receive the same system prompt, private input, schema name, and JSON schema from `extract.Request`. Adapters verify the request is byte-for-byte equal to the supported embedded contract before network access, matching the current Bedrock behavior.

OpenAI uses one stateless Responses request with strict `text.format` JSON schema. It always sets `store: false`, disables background operation, supplies no conversation or previous-response ID, and enables no tools, file storage, hosted execution, or remote connectors. `store: false` avoids Responses application-state storage, but documentation must still distinguish that request setting from an organization-level Zero Data Retention agreement.

Anthropic uses one stateless Messages request with `output_config.format` set to the JSON schema. It enables no tools, files, batches, prompt-caching option, or managed-agent feature. A `refusal` stop reason, `max_tokens`, missing usage, multiple or non-text output blocks, invalid JSON, or mismatched returned model is a bounded invalid/incomplete outcome.

Bedrock retains Converse structured output and its existing no-runtime-logging-configuration behavior. Restricted mode adds the control-plane disclosure gate; it does not enable or change AWS logging.

Each adapter owns a bounded retry loop and disables or neutralizes SDK-level retries so configured attempts are exact rather than multiplied. Retries are allowed only for provider throttling, transient server/unavailable responses, and retryable transport failures. Authentication, authorization, invalid request, refusal, invalid output, and context cancellation are terminal. Backoff must respect the caller context and the existing ingestion attempt timeout.

## Provenance and migration 00010

Migration `00010_model_provider_provenance.sql` adds:

- `model_provider` to `stacks.extraction_runs` and `stacks.analysis_runs`;
- `data_mode` to those runs so new external disclosures are auditable;
- provider-aware constraints for Bedrock region: Bedrock rows require a non-empty region, while direct OpenAI and Anthropic rows require no region;
- bounded values for new rows, while migrated rows use an explicit `legacy` data mode rather than claiming a policy that was not enforced at invocation time.

Existing rows are backfilled with `model_provider = 'bedrock'` and `data_mode = 'legacy'`. The existing `bedrock_region` column remains because it is accurate Bedrock-specific provenance; it becomes nullable only for direct-provider rows. Renaming it to a vague location field would lose meaning.

Extraction and analysis identities gain `Provider`, with provider-specific validation. `DataMode` travels as separate run-completion provenance: it is recorded when a model is actually invoked, but it is not part of the semantic cache identity and does not prevent reuse of a result that requires no new disclosure.

Digest compatibility follows two branches:

- Bedrock identities use the existing digest byte sequence, so current Bedrock extraction and analysis results remain cache-compatible.
- OpenAI and Anthropic identities use a new provider-qualified digest namespace containing provider, model ID, token limit, prompt/schema or policy version, and existing immutable inputs.

This prevents cross-provider cache collisions without forcing existing Bedrock data to reprocess. A cached Bedrock result may be reused without a new disclosure; any later Bedrock invocation records the active data mode on its new run. Existing `legacy` rows remain immutable audit records.

Signals continue to retain extraction model ID, prompt version, and their observation's extraction-run link. The run supplies provider, region, and disclosure-mode provenance, avoiding a second independently mutable provider field on each signal.

## Observability and errors

Generalize Bedrock-only invocation telemetry to bounded model telemetry:

- metric and span names use `stacks.model.*`;
- provider is one of the three configured enum values;
- model ID, prompt version, outcome, attempt count, token counts, and latency remain bounded operational metadata;
- data mode may be recorded as a two-value attribute;
- API keys, request/response bodies, prompts, Drive metadata, names, emails, citations, and raw provider errors are excluded.

Provider error bodies are not returned verbatim because they may echo request material. Adapters map them to bounded outcomes and preserve only safe operation context. Successful manually owned spans continue to use `observability.FinishSpan` and explicit `OK` status.

## Google Drive boundary

Normal authentication and ingestion retain the existing read-only Drive and Docs scopes. The personal folder is selected only through `STACKS_GOOGLE_FOLDER_ID`, remains limited to direct Google Doc children, and follows the existing tab/title/date rules.

Creating synthetic Google Docs is a separate optional follow-up. If implemented, it will use a distinct command and distinct write-authorized token path, accept only synthetic fixture input, and never share the sync command's read-only token. Provider abstraction work does not silently broaden OAuth permissions.

Until that follow-up is approved, live personal-Drive acceptance uses documents created manually by the user or another explicitly authorized tool. Private company documents are not copied into the personal folder or used in direct-provider tests.

## Test strategy

Implementation proceeds test-first.

1. Configuration tests prove all mode/provider combinations, explicit model requirements, provider-only settings, unsupported legacy variables, and validation before boundary construction.
2. Adapter contract tests use local fake HTTP servers or SDK transports. They assert exact structured-output requests, OpenAI `store: false`, no optional tools/state, response metadata, refusal/truncation handling, safe error redaction, exact retry counts, and cancellation.
3. Application wiring tests prove restricted non-Bedrock commands construct no external boundary and restricted Bedrock checks logging before Google access.
4. Digest tests lock existing Bedrock byte compatibility and prove provider separation for identical model IDs and inputs.
5. PostgreSQL migration tests upgrade schemas through 00010, verify backfills and constraints, and exercise extraction leases, retry/resume, admission quarantine, analysis caching, and provider provenance.
6. Existing synthetic full tests, race tests, Staticcheck, build verification, migration status, and doctor tests remain required.
7. Personal-provider acceptance uses only synthetic content in the configured personal Drive folder. OpenAI and Anthropic are tested separately with an explicitly configured model and a bounded invocation budget.

## Acceptance boundaries

Passing local tests and synthetic personal-provider invocations establish only:

- local PostgreSQL/pgvector integration;
- Google Drive ingestion of the synthetic personal corpus;
- direct OpenAI and Anthropic structured extraction/analysis behavior;
- provider provenance, retries, resume behavior, and disclosure-mode enforcement.

They do not establish:

- Bedrock runtime quota or successful Bedrock inference;
- organization-approved handling of company IP;
- company Google Drive OAuth/IAM acceptance;
- Bedrock invocation-logging inspection permissions in the target account;
- provider contractual retention guarantees beyond the tested request configuration.

Those remain separately reported acceptance gates.
