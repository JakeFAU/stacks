CREATE TABLE stacks_core.entities (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    kind text NOT NULL CHECK (kind IN ('person')),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    recorded_at timestamptz(6) NOT NULL
);

CREATE TABLE stacks_core.mentions (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    evidence_id text NOT NULL
        REFERENCES stacks_core.evidence_spans (id),
    derivation_run_id text NOT NULL CHECK (btrim(derivation_run_id) <> ''),
    surface text NOT NULL CHECK (btrim(surface) <> ''),
    normalized_name text NOT NULL CHECK (btrim(normalized_name) <> ''),
    proposed_email text NOT NULL DEFAULT '',
    proposed_email_evidence_id text
        REFERENCES stacks_core.evidence_spans (id),
    role text NOT NULL CHECK (btrim(role) <> ''),
    recorded_at timestamptz(6) NOT NULL,
    CONSTRAINT mentions_proposed_email_evidence_check CHECK (
        (proposed_email = '' AND proposed_email_evidence_id IS NULL)
        OR
        (proposed_email <> '' AND proposed_email_evidence_id IS NOT NULL)
    )
);

CREATE TABLE stacks_core.resolution_proposals (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    mention_id text NOT NULL
        REFERENCES stacks_core.mentions (id),
    reason_code text NOT NULL CHECK (btrim(reason_code) <> ''),
    recorded_at timestamptz(6) NOT NULL
);

CREATE TABLE stacks_core.resolution_proposal_evidence (
    proposal_id text NOT NULL
        REFERENCES stacks_core.resolution_proposals (id),
    evidence_id text NOT NULL
        REFERENCES stacks_core.evidence_spans (id),
    evidence_order integer NOT NULL CHECK (evidence_order >= 0),
    PRIMARY KEY (proposal_id, evidence_id),
    UNIQUE (proposal_id, evidence_order)
);

CREATE TABLE stacks_core.resolution_candidates (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    proposal_id text NOT NULL
        REFERENCES stacks_core.resolution_proposals (id),
    entity_id text NOT NULL
        REFERENCES stacks_core.entities (id),
    candidate_rank integer NOT NULL CHECK (candidate_rank > 0),
    confidence double precision NOT NULL
        CHECK (confidence >= 0 AND confidence <= 1),
    reason_code text NOT NULL CHECK (btrim(reason_code) <> ''),
    source_kind text NOT NULL CHECK (btrim(source_kind) <> ''),
    source_reference text NOT NULL CHECK (btrim(source_reference) <> ''),
    recorded_at timestamptz(6) NOT NULL,
    UNIQUE (proposal_id, candidate_rank)
);

CREATE TABLE stacks_core.resolution_decisions (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    proposal_id text NOT NULL
        REFERENCES stacks_core.resolution_proposals (id),
    outcome text NOT NULL CHECK (outcome IN ('accepted', 'rejected')),
    entity_id text
        REFERENCES stacks_core.entities (id),
    authority text NOT NULL CHECK (authority IN ('automatic', 'reviewer')),
    reason_code text NOT NULL CHECK (btrim(reason_code) <> ''),
    recorded_at timestamptz(6) NOT NULL,
    supersedes_id text
        REFERENCES stacks_core.resolution_decisions (id),
    digest_version text NOT NULL CHECK (btrim(digest_version) <> ''),
    digest bytea NOT NULL
        CONSTRAINT resolution_decisions_digest_check
        CHECK (octet_length(digest) = 32),
    CONSTRAINT resolution_decisions_outcome_entity_check CHECK (
        (outcome = 'accepted' AND entity_id IS NOT NULL)
        OR
        (outcome = 'rejected' AND entity_id IS NULL)
    ),
    CONSTRAINT resolution_decisions_supersedes_check
        CHECK (supersedes_id IS NULL OR supersedes_id <> id)
);

CREATE UNIQUE INDEX resolution_decisions_one_initial
    ON stacks_core.resolution_decisions (proposal_id)
    WHERE supersedes_id IS NULL;

CREATE UNIQUE INDEX resolution_decisions_one_successor
    ON stacks_core.resolution_decisions (supersedes_id)
    WHERE supersedes_id IS NOT NULL;

CREATE TABLE stacks_core.entity_alias_assertions (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    decision_id text NOT NULL
        REFERENCES stacks_core.resolution_decisions (id),
    entity_id text NOT NULL
        REFERENCES stacks_core.entities (id),
    alias_type text NOT NULL CHECK (alias_type IN ('name', 'email')),
    alias_value text NOT NULL CHECK (btrim(alias_value) <> ''),
    recorded_at timestamptz(6) NOT NULL,
    UNIQUE (decision_id, alias_type, alias_value)
);

CREATE TABLE stacks_core.admission_targets (
    target_kind text NOT NULL CHECK (
        target_kind IN (
            'extraction_run',
            'mention',
            'observation',
            'identity_decision'
        )
    ),
    target_id text NOT NULL CHECK (btrim(target_id) <> ''),
    recorded_at timestamptz(6) NOT NULL,
    PRIMARY KEY (target_kind, target_id)
);

CREATE TABLE stacks_core.admission_decisions (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    target_kind text NOT NULL,
    target_id text NOT NULL,
    outcome text NOT NULL CHECK (
        outcome IN ('admitted', 'quarantined', 'retired')
    ),
    reason_code text NOT NULL CHECK (btrim(reason_code) <> ''),
    authority text NOT NULL CHECK (
        authority IN ('automatic', 'reviewer', 'policy')
    ),
    recorded_at timestamptz(6) NOT NULL,
    supersedes_id text
        REFERENCES stacks_core.admission_decisions (id),
    digest_version text NOT NULL CHECK (btrim(digest_version) <> ''),
    digest bytea NOT NULL
        CONSTRAINT admission_decisions_digest_check
        CHECK (octet_length(digest) = 32),
    FOREIGN KEY (target_kind, target_id)
        REFERENCES stacks_core.admission_targets (target_kind, target_id),
    CONSTRAINT admission_decisions_supersedes_check
        CHECK (supersedes_id IS NULL OR supersedes_id <> id)
);

CREATE UNIQUE INDEX admission_decisions_one_initial
    ON stacks_core.admission_decisions (target_kind, target_id)
    WHERE supersedes_id IS NULL;

CREATE UNIQUE INDEX admission_decisions_one_successor
    ON stacks_core.admission_decisions (supersedes_id)
    WHERE supersedes_id IS NOT NULL;
