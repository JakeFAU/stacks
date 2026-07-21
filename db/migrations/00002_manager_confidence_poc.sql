-- +goose Up
CREATE TABLE stacks.source_documents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider text NOT NULL CHECK (btrim(provider) <> ''),
    provider_document_id text NOT NULL CHECK (btrim(provider_document_id) <> ''),
    recorded_at timestamptz NOT NULL,
    UNIQUE (provider, provider_document_id)
);

CREATE TABLE stacks.document_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_document_id uuid NOT NULL REFERENCES stacks.source_documents(id),
    digest bytea NOT NULL CHECK (octet_length(digest) = 32),
    recorded_at timestamptz NOT NULL,
    processing_status text NOT NULL DEFAULT 'pending' CHECK (processing_status IN ('pending', 'complete', 'incomplete', 'failed')),
    UNIQUE (source_document_id, digest)
);

CREATE TABLE stacks.document_tabs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_version_id uuid NOT NULL REFERENCES stacks.document_versions(id),
    provider_tab_id text NOT NULL CHECK (btrim(provider_tab_id) <> ''),
    title text NOT NULL CHECK (btrim(title) <> ''),
    parent_provider_tab_id text NOT NULL DEFAULT '',
    title_path text[] NOT NULL,
    display_order integer NOT NULL CHECK (display_order >= 0),
    role text NOT NULL CHECK (role IN ('other', 'transcript', 'gemini-notes')),
    content text NOT NULL,
    content_digest bytea NOT NULL CHECK (octet_length(content_digest) = 32),
    UNIQUE (document_version_id, provider_tab_id),
    UNIQUE (document_version_id, display_order)
);

CREATE TABLE stacks.evidence_spans (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_tab_id uuid NOT NULL REFERENCES stacks.document_tabs(id),
    start_offset integer NOT NULL CHECK (start_offset >= 0),
    end_offset integer NOT NULL CHECK (end_offset > start_offset),
    quote text NOT NULL CHECK (quote <> ''),
    UNIQUE (document_tab_id, start_offset, end_offset)
);

CREATE TABLE stacks.entities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind text NOT NULL CHECK (btrim(kind) <> ''),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    recorded_at timestamptz NOT NULL
);

CREATE TABLE stacks.entity_aliases (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id uuid NOT NULL REFERENCES stacks.entities(id),
    normalized_value text NOT NULL CHECK (btrim(normalized_value) <> ''),
    alias_type text NOT NULL CHECK (alias_type IN ('name', 'email')),
    recorded_at timestamptz NOT NULL,
    UNIQUE (entity_id, normalized_value, alias_type)
);

CREATE TABLE stacks.mentions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    evidence_span_id uuid NOT NULL REFERENCES stacks.evidence_spans(id),
    surface text NOT NULL CHECK (btrim(surface) <> ''),
    role text NOT NULL CHECK (role IN ('speaker', 'reference')),
    recorded_at timestamptz NOT NULL
);

CREATE TABLE stacks.resolution_proposals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mention_id uuid NOT NULL REFERENCES stacks.mentions(id),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'resolved', 'rejected', 'superseded')),
    derivation text NOT NULL CHECK (btrim(derivation) <> ''),
    recorded_at timestamptz NOT NULL
);

CREATE TABLE stacks.resolution_candidates (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    proposal_id uuid NOT NULL REFERENCES stacks.resolution_proposals(id),
    entity_id uuid NOT NULL REFERENCES stacks.entities(id),
    rank integer NOT NULL CHECK (rank >= 0),
    confidence double precision CHECK (confidence IS NULL OR isfinite(confidence)),
    reason text NOT NULL DEFAULT '',
    UNIQUE (proposal_id, entity_id),
    UNIQUE (proposal_id, rank)
);

CREATE TABLE stacks.resolution_decisions (
    id uuid PRIMARY KEY,
    proposal_id uuid NOT NULL REFERENCES stacks.resolution_proposals(id),
    outcome text NOT NULL CHECK (outcome IN ('accepted', 'rejected', 'created')),
    entity_id uuid REFERENCES stacks.entities(id),
    supersedes_id uuid UNIQUE REFERENCES stacks.resolution_decisions(id),
    superseded_by_id uuid UNIQUE REFERENCES stacks.resolution_decisions(id),
    recorded_at timestamptz NOT NULL,
    CHECK ((outcome = 'rejected' AND entity_id IS NULL) OR (outcome IN ('accepted', 'created') AND entity_id IS NOT NULL))
);

CREATE UNIQUE INDEX resolution_decisions_one_effective_per_proposal
    ON stacks.resolution_decisions (proposal_id)
    WHERE superseded_by_id IS NULL;

CREATE TABLE stacks.observations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_entity_id uuid REFERENCES stacks.entities(id),
    object_entity_id uuid REFERENCES stacks.entities(id),
    predicate text NOT NULL CHECK (btrim(predicate) <> ''),
    valid_start timestamptz,
    valid_end timestamptz,
    recorded_at timestamptz NOT NULL,
    derivation text NOT NULL CHECK (btrim(derivation) <> ''),
    epistemic_status text NOT NULL CHECK (epistemic_status IN ('observed', 'inferred', 'hypothesized', 'validated_structurally', 'validated_empirically', 'rejected')),
    confidence double precision CHECK (confidence IS NULL OR isfinite(confidence)),
    CHECK (valid_end IS NULL OR valid_start IS NOT NULL),
    CHECK (valid_end IS NULL OR valid_end >= valid_start)
);

CREATE TABLE stacks.observation_evidence (
    observation_id uuid NOT NULL REFERENCES stacks.observations(id),
    evidence_span_id uuid NOT NULL REFERENCES stacks.evidence_spans(id),
    PRIMARY KEY (observation_id, evidence_span_id)
);

CREATE TABLE stacks.interaction_signals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    observation_id uuid NOT NULL UNIQUE REFERENCES stacks.observations(id),
    category text NOT NULL CHECK (category IN ('delegation_autonomy', 'scrutiny_correction', 'endorsement_trust', 'support_advocacy', 'future_responsibility')),
    direction text NOT NULL CHECK (direction IN ('strengthening', 'weakening', 'mixed', 'unclear')),
    extraction_model_id text NOT NULL CHECK (btrim(extraction_model_id) <> ''),
    prompt_version text NOT NULL CHECK (btrim(prompt_version) <> ''),
    rationale text NOT NULL DEFAULT '',
    confidence double precision NOT NULL CHECK (isfinite(confidence))
);

CREATE TABLE stacks.signal_evidence (
    signal_id uuid NOT NULL REFERENCES stacks.interaction_signals(id),
    evidence_span_id uuid NOT NULL REFERENCES stacks.evidence_spans(id),
    role text NOT NULL CHECK (role IN ('supporting', 'contradicting')),
    PRIMARY KEY (signal_id, evidence_span_id, role)
);

CREATE TABLE stacks.analysis_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_entity_id uuid NOT NULL REFERENCES stacks.entities(id),
    manager_entity_id uuid NOT NULL REFERENCES stacks.entities(id),
    input_digest bytea NOT NULL UNIQUE CHECK (octet_length(input_digest) = 32),
    analysis_prompt_version text NOT NULL CHECK (btrim(analysis_prompt_version) <> ''),
    policy_version text NOT NULL CHECK (btrim(policy_version) <> ''),
    state text NOT NULL CHECK (state IN ('pending', 'complete', 'failed')),
    recorded_at timestamptz NOT NULL,
    completed_at timestamptz,
    hypothesis text NOT NULL DEFAULT '',
    report_state text NOT NULL DEFAULT ''
);

CREATE TABLE stacks.analysis_inputs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    analysis_run_id uuid NOT NULL REFERENCES stacks.analysis_runs(id),
    input_kind text NOT NULL CHECK (input_kind IN ('document_version', 'document_tab', 'observation', 'signal', 'resolution_decision')),
    input_id uuid NOT NULL,
    input_digest bytea NOT NULL CHECK (octet_length(input_digest) = 32),
    position integer NOT NULL CHECK (position >= 0),
    UNIQUE (analysis_run_id, input_digest),
    UNIQUE (analysis_run_id, position)
);
