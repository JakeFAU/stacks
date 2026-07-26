CREATE TABLE stacks_core.extraction_runs (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    document_version_id text NOT NULL
        REFERENCES stacks_core.document_versions (id),
    derivation_digest_version text NOT NULL
        CHECK (btrim(derivation_digest_version) <> ''),
    derivation_digest bytea NOT NULL
        CHECK (octet_length(derivation_digest) = 32),
    method text NOT NULL CHECK (btrim(method) <> ''),
    version text NOT NULL CHECK (btrim(version) <> ''),
    provider text NOT NULL CHECK (btrim(provider) <> ''),
    data_mode text NOT NULL CHECK (btrim(data_mode) <> ''),
    model text NOT NULL CHECK (btrim(model) <> ''),
    prompt_version text NOT NULL CHECK (btrim(prompt_version) <> ''),
    schema_digest bytea NOT NULL
        CHECK (octet_length(schema_digest) = 32),
    max_output_tokens integer NOT NULL CHECK (max_output_tokens > 0),
    recorded_at timestamptz(6) NOT NULL,
    state text NOT NULL CHECK (state IN ('active', 'failed', 'completed')),
    completed_at timestamptz(6),
    write_set_digest_version text,
    write_set_digest bytea,
    UNIQUE (
        document_version_id,
        derivation_digest_version,
        derivation_digest
    ),
    CONSTRAINT extraction_runs_completion_check CHECK (
        (
            state = 'completed'
            AND completed_at IS NOT NULL
            AND write_set_digest_version IS NOT NULL
            AND btrim(write_set_digest_version) <> ''
            AND write_set_digest IS NOT NULL
            AND octet_length(write_set_digest) = 32
        )
        OR
        (
            state <> 'completed'
            AND completed_at IS NULL
            AND write_set_digest_version IS NULL
            AND write_set_digest IS NULL
        )
    )
);

CREATE TABLE stacks_core.extraction_attempts (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    run_id text NOT NULL
        REFERENCES stacks_core.extraction_runs (id),
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    owner text NOT NULL CHECK (btrim(owner) <> ''),
    claimed_at timestamptz(6) NOT NULL,
    lease_expires_at timestamptz(6) NOT NULL,
    state text NOT NULL CHECK (
        state IN ('active', 'completed', 'failed', 'canceled', 'expired')
    ),
    terminal_at timestamptz(6),
    failure_code text,
    UNIQUE (run_id, attempt_number),
    CONSTRAINT extraction_attempts_lease_check
        CHECK (lease_expires_at > claimed_at),
    CONSTRAINT extraction_attempts_terminal_check CHECK (
        (
            state = 'active'
            AND terminal_at IS NULL
            AND failure_code IS NULL
        )
        OR
        (
            state = 'failed'
            AND terminal_at IS NOT NULL
            AND failure_code IS NOT NULL
            AND btrim(failure_code) <> ''
        )
        OR
        (
            state IN ('completed', 'canceled', 'expired')
            AND terminal_at IS NOT NULL
            AND failure_code IS NULL
        )
    ),
    CONSTRAINT extraction_attempts_terminal_order_check CHECK (
        terminal_at IS NULL
        OR (
            terminal_at >= claimed_at
            AND terminal_at <= lease_expires_at
        )
    )
);

CREATE UNIQUE INDEX extraction_attempts_one_active
    ON stacks_core.extraction_attempts (run_id)
    WHERE state = 'active';

CREATE TABLE stacks_core.observations (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    subject_kind text NOT NULL CHECK (
        subject_kind IN (
            'absent',
            'text',
            'mention',
            'entity',
            'grounded_entity'
        )
    ),
    subject_text text,
    subject_mention_id text
        REFERENCES stacks_core.mentions (id),
    subject_entity_id text
        REFERENCES stacks_core.entities (id),
    subject_grounding_mention_id text
        REFERENCES stacks_core.mentions (id),
    predicate text NOT NULL CHECK (btrim(predicate) <> ''),
    object_kind text NOT NULL CHECK (
        object_kind IN (
            'absent',
            'text',
            'mention',
            'entity',
            'grounded_entity'
        )
    ),
    object_text text,
    object_mention_id text
        REFERENCES stacks_core.mentions (id),
    object_entity_id text
        REFERENCES stacks_core.entities (id),
    object_grounding_mention_id text
        REFERENCES stacks_core.mentions (id),
    temporal_kind text NOT NULL CHECK (
        temporal_kind IN ('unknown', 'instant', 'interval', 'window')
    ),
    has_start boolean NOT NULL,
    valid_start timestamptz(6),
    has_end boolean NOT NULL,
    valid_end timestamptz(6),
    recorded_at timestamptz(6) NOT NULL,
    derivation_method text NOT NULL CHECK (btrim(derivation_method) <> ''),
    derivation_version text NOT NULL CHECK (btrim(derivation_version) <> ''),
    derivation_run_id text
        REFERENCES stacks_core.extraction_runs (id),
    derivation_model text,
    derivation_prompt_version text,
    epistemic_status text NOT NULL CHECK (
        epistemic_status IN (
            'observed',
            'inferred',
            'hypothesized',
            'validated_structurally',
            'validated_empirically',
            'rejected'
        )
    ),
    confidence_value double precision,
    confidence_scale text,
    digest_version text NOT NULL CHECK (btrim(digest_version) <> ''),
    digest bytea NOT NULL CHECK (octet_length(digest) = 32),
    CONSTRAINT observations_subject_shape_check CHECK (
        (
            subject_kind = 'absent'
            AND subject_text IS NULL
            AND subject_mention_id IS NULL
            AND subject_entity_id IS NULL
            AND subject_grounding_mention_id IS NULL
        )
        OR
        (
            subject_kind = 'text'
            AND subject_text IS NOT NULL
            AND btrim(subject_text) <> ''
            AND subject_mention_id IS NULL
            AND subject_entity_id IS NULL
            AND subject_grounding_mention_id IS NULL
        )
        OR
        (
            subject_kind = 'mention'
            AND subject_text IS NULL
            AND subject_mention_id IS NOT NULL
            AND subject_entity_id IS NULL
            AND subject_grounding_mention_id IS NULL
        )
        OR
        (
            subject_kind = 'entity'
            AND subject_text IS NULL
            AND subject_mention_id IS NULL
            AND subject_entity_id IS NOT NULL
            AND subject_grounding_mention_id IS NULL
        )
        OR
        (
            subject_kind = 'grounded_entity'
            AND subject_text IS NULL
            AND subject_mention_id IS NULL
            AND subject_entity_id IS NOT NULL
            AND subject_grounding_mention_id IS NOT NULL
        )
    ),
    CONSTRAINT observations_object_shape_check CHECK (
        (
            object_kind = 'absent'
            AND object_text IS NULL
            AND object_mention_id IS NULL
            AND object_entity_id IS NULL
            AND object_grounding_mention_id IS NULL
        )
        OR
        (
            object_kind = 'text'
            AND object_text IS NOT NULL
            AND btrim(object_text) <> ''
            AND object_mention_id IS NULL
            AND object_entity_id IS NULL
            AND object_grounding_mention_id IS NULL
        )
        OR
        (
            object_kind = 'mention'
            AND object_text IS NULL
            AND object_mention_id IS NOT NULL
            AND object_entity_id IS NULL
            AND object_grounding_mention_id IS NULL
        )
        OR
        (
            object_kind = 'entity'
            AND object_text IS NULL
            AND object_mention_id IS NULL
            AND object_entity_id IS NOT NULL
            AND object_grounding_mention_id IS NULL
        )
        OR
        (
            object_kind = 'grounded_entity'
            AND object_text IS NULL
            AND object_mention_id IS NULL
            AND object_entity_id IS NOT NULL
            AND object_grounding_mention_id IS NOT NULL
        )
    ),
    CONSTRAINT observations_temporal_shape_check CHECK (
        (
            temporal_kind = 'unknown'
            AND NOT has_start
            AND valid_start IS NULL
            AND NOT has_end
            AND valid_end IS NULL
        )
        OR
        (
            temporal_kind = 'instant'
            AND has_start
            AND valid_start IS NOT NULL
            AND NOT has_end
            AND valid_end IS NULL
        )
        OR
        (
            temporal_kind = 'interval'
            AND (has_start OR has_end)
            AND has_start = (valid_start IS NOT NULL)
            AND has_end = (valid_end IS NOT NULL)
            AND (
                NOT (has_start AND has_end)
                OR valid_end > valid_start
            )
        )
        OR
        (
            temporal_kind = 'window'
            AND has_start
            AND valid_start IS NOT NULL
            AND has_end
            AND valid_end IS NOT NULL
            AND valid_end > valid_start
        )
    ),
    CONSTRAINT observations_derivation_model_check CHECK (
        (
            derivation_model IS NULL
            AND derivation_prompt_version IS NULL
        )
        OR
        (
            derivation_model IS NOT NULL
            AND btrim(derivation_model) <> ''
            AND derivation_prompt_version IS NOT NULL
            AND btrim(derivation_prompt_version) <> ''
        )
    ),
    CONSTRAINT observations_confidence_check CHECK (
        (
            confidence_value IS NULL
            AND confidence_scale IS NULL
        )
        OR
        (
            confidence_value IS NOT NULL
            AND confidence_value >= 0
            AND confidence_value <= 1
            AND confidence_scale = 'unit_interval'
        )
    )
);

CREATE TABLE stacks_core.observation_evidence (
    observation_id text NOT NULL
        REFERENCES stacks_core.observations (id),
    evidence_id text NOT NULL
        REFERENCES stacks_core.evidence_spans (id),
    role text NOT NULL CHECK (role IN ('supporting', 'contradicting')),
    PRIMARY KEY (observation_id, evidence_id, role)
);

CREATE FUNCTION stacks_core.enforce_observation_cited()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    checked_observation_id text;
BEGIN
    IF TG_TABLE_NAME = 'observations' THEN
        checked_observation_id := NEW.id;
    ELSE
        checked_observation_id := OLD.observation_id;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM stacks_core.observations
        WHERE id = checked_observation_id
    ) AND NOT EXISTS (
        SELECT 1
        FROM stacks_core.observation_evidence
        WHERE observation_id = checked_observation_id
    ) THEN
        RAISE EXCEPTION 'canonical observation requires evidence'
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$function$;

CREATE CONSTRAINT TRIGGER observations_require_evidence
AFTER INSERT OR UPDATE ON stacks_core.observations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION stacks_core.enforce_observation_cited();

CREATE CONSTRAINT TRIGGER observation_evidence_preserves_citation
AFTER DELETE ON stacks_core.observation_evidence
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION stacks_core.enforce_observation_cited();
