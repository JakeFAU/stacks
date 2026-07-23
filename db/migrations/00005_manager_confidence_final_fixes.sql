-- +goose Up
ALTER TABLE stacks.source_documents
    ADD COLUMN title text NOT NULL DEFAULT '',
    ADD COLUMN locator text NOT NULL DEFAULT '';

ALTER TABLE stacks.document_versions
    ADD COLUMN title text NOT NULL DEFAULT '',
    ADD COLUMN locator text NOT NULL DEFAULT '',
    ADD COLUMN provider_version text NOT NULL DEFAULT '',
    ADD COLUMN provider_revision text NOT NULL DEFAULT '',
    ADD COLUMN provider_modified_at timestamptz,
    ADD COLUMN source_meeting_time timestamptz;

CREATE TABLE stacks.extraction_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_version_id uuid NOT NULL REFERENCES stacks.document_versions(id),
    derivation_digest bytea NOT NULL CHECK (octet_length(derivation_digest) = 32),
    model_id text NOT NULL CHECK (btrim(model_id) <> ''),
    bedrock_region text NOT NULL CHECK (btrim(bedrock_region) <> ''),
    max_output_tokens integer NOT NULL CHECK (max_output_tokens > 0),
    prompt_version text NOT NULL CHECK (btrim(prompt_version) <> ''),
    schema_digest bytea NOT NULL CHECK (octet_length(schema_digest) = 32),
    processing_status text NOT NULL DEFAULT 'pending'
        CHECK (processing_status IN ('pending', 'complete', 'incomplete', 'failed')),
    failure_code text CHECK (failure_code IS NULL OR failure_code IN (
        'source_error', 'invalid_source', 'model_error', 'invalid_output', 'storage_error'
    )),
    retry_count integer NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    lease_owner uuid,
    lease_expires_at timestamptz,
    completed_by_owner uuid,
    recorded_at timestamptz NOT NULL,
    completed_at timestamptz,
    CONSTRAINT extraction_runs_state_ownership CHECK (
        (processing_status = 'pending' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL
            AND failure_code IS NULL AND completed_by_owner IS NULL AND completed_at IS NULL) OR
        (processing_status = 'complete' AND lease_owner IS NULL AND lease_expires_at IS NULL
            AND failure_code IS NULL AND completed_by_owner IS NOT NULL AND completed_at IS NOT NULL) OR
        (processing_status IN ('incomplete', 'failed') AND lease_owner IS NULL AND lease_expires_at IS NULL
            AND failure_code IS NOT NULL AND completed_by_owner IS NULL AND completed_at IS NULL)
    ),
    UNIQUE (document_version_id, derivation_digest)
);

ALTER TABLE stacks.mentions
    ADD COLUMN extraction_run_id uuid REFERENCES stacks.extraction_runs(id),
    ADD COLUMN normalized_name text NOT NULL DEFAULT '',
    ADD COLUMN normalized_email text NOT NULL DEFAULT '';

ALTER TABLE stacks.mentions
    DROP CONSTRAINT mentions_evidence_span_id_surface_role_key;

CREATE UNIQUE INDEX mentions_legacy_identity
    ON stacks.mentions (evidence_span_id, surface, role)
    WHERE extraction_run_id IS NULL;

CREATE UNIQUE INDEX mentions_derivation_identity
    ON stacks.mentions (extraction_run_id, evidence_span_id, surface, role)
    WHERE extraction_run_id IS NOT NULL;

ALTER TABLE stacks.observations
    ADD COLUMN extraction_run_id uuid REFERENCES stacks.extraction_runs(id);

UPDATE stacks.mentions
SET normalized_name = lower(regexp_replace(btrim(surface), '\s+', ' ', 'g'));

CREATE TABLE stacks.entity_alias_assertions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    decision_id uuid NOT NULL REFERENCES stacks.resolution_decisions(id),
    entity_id uuid NOT NULL REFERENCES stacks.entities(id),
    normalized_value text NOT NULL CHECK (btrim(normalized_value) <> ''),
    alias_type text NOT NULL CHECK (alias_type IN ('name', 'email')),
    recorded_at timestamptz NOT NULL,
    UNIQUE (decision_id, normalized_value, alias_type)
);

-- Existing aliases become authoritative only through a currently effective
-- accepted decision. Standalone aliases remain as legacy audit records.
INSERT INTO stacks.entity_alias_assertions
    (decision_id, entity_id, normalized_value, alias_type, recorded_at)
SELECT decision.id, decision.entity_id, mention.normalized_name, 'name', decision.recorded_at
FROM stacks.resolution_decisions AS decision
JOIN stacks.resolution_proposals AS proposal ON proposal.id = decision.proposal_id
JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
WHERE decision.superseded_by_id IS NULL
  AND decision.outcome IN ('accepted', 'created')
  AND mention.normalized_name <> ''
ON CONFLICT (decision_id, normalized_value, alias_type) DO NOTHING;

INSERT INTO stacks.entity_alias_assertions
    (decision_id, entity_id, normalized_value, alias_type, recorded_at)
SELECT decision.id, decision.entity_id, alias.normalized_value, alias.alias_type, decision.recorded_at
FROM stacks.resolution_decisions AS decision
JOIN stacks.entity_aliases AS alias ON alias.entity_id = decision.entity_id
WHERE decision.superseded_by_id IS NULL
  AND decision.outcome IN ('accepted', 'created')
ON CONFLICT (decision_id, normalized_value, alias_type) DO NOTHING;

-- Historical model-authored payloads remain immutable audit records. Migration
-- 00006 marks pre-fix derived state non-admissible instead of rewriting it.
