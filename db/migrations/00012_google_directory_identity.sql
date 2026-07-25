-- +goose Up
CREATE TABLE stacks.directory_profile_snapshots (
    id uuid PRIMARY KEY,
    provider text NOT NULL CHECK (provider IN ('google_people')),
    provider_subject_id text NOT NULL CHECK (btrim(provider_subject_id) <> ''),
    source_type text NOT NULL CHECK (source_type IN ('domain_profile', 'domain_contact')),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    observed_at timestamptz,
    recorded_at timestamptz NOT NULL,
    digest bytea NOT NULL UNIQUE CHECK (octet_length(digest) = 32),
    UNIQUE (provider, provider_subject_id, digest)
);

CREATE TABLE stacks.directory_profile_emails (
    snapshot_id uuid NOT NULL REFERENCES stacks.directory_profile_snapshots(id),
    normalized_email text NOT NULL CHECK (
        normalized_email = lower(btrim(normalized_email))
        AND normalized_email LIKE '%@%'
    ),
    is_primary boolean NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    PRIMARY KEY (snapshot_id, normalized_email),
    UNIQUE (snapshot_id, position)
);

CREATE TABLE stacks.directory_lookup_attempts (
    id uuid PRIMARY KEY,
    mention_id uuid NOT NULL REFERENCES stacks.mentions(id),
    provider text NOT NULL CHECK (provider IN ('google_people')),
    query_kind text NOT NULL CHECK (query_kind IN ('email', 'name')),
    email_evidence text NOT NULL CHECK (
        email_evidence IN ('none', 'source_bound', 'citation_verified', 'reviewer_supplied')
    ),
    query_digest bytea NOT NULL CHECK (octet_length(query_digest) = 32),
    policy_version text NOT NULL CHECK (btrim(policy_version) <> ''),
    outcome text NOT NULL CHECK (outcome IN (
        'matched', 'no_match', 'ambiguous', 'review', 'disabled',
        'not_configured', 'unauthorized', 'forbidden', 'rate_limited',
        'unavailable', 'invalid_response', 'result_limit_exceeded'
    )),
    attempt_count integer NOT NULL CHECK (attempt_count >= 0),
    retry_after timestamptz,
    recorded_at timestamptz NOT NULL,
    digest bytea NOT NULL UNIQUE CHECK (octet_length(digest) = 32)
);

CREATE INDEX directory_lookup_attempts_mention
    ON stacks.directory_lookup_attempts (mention_id);

CREATE TABLE stacks.directory_lookup_matches (
    lookup_attempt_id uuid NOT NULL REFERENCES stacks.directory_lookup_attempts(id),
    snapshot_id uuid NOT NULL REFERENCES stacks.directory_profile_snapshots(id),
    rank integer NOT NULL CHECK (rank >= 0),
    reason text NOT NULL CHECK (reason IN ('exact_email', 'name_candidate')),
    PRIMARY KEY (lookup_attempt_id, snapshot_id),
    UNIQUE (lookup_attempt_id, rank)
);

CREATE INDEX directory_lookup_matches_snapshot
    ON stacks.directory_lookup_matches (snapshot_id);

ALTER TABLE stacks.resolution_candidates
    ALTER COLUMN entity_id DROP NOT NULL,
    ADD COLUMN directory_profile_snapshot_id uuid
        REFERENCES stacks.directory_profile_snapshots(id),
    ADD CONSTRAINT resolution_candidates_one_source CHECK (
        (entity_id IS NOT NULL)::integer
        + (directory_profile_snapshot_id IS NOT NULL)::integer = 1
    );

CREATE UNIQUE INDEX resolution_candidates_directory_profile_unique
    ON stacks.resolution_candidates (proposal_id, directory_profile_snapshot_id)
    WHERE directory_profile_snapshot_id IS NOT NULL;

CREATE INDEX resolution_candidates_directory_profile
    ON stacks.resolution_candidates (directory_profile_snapshot_id)
    WHERE directory_profile_snapshot_id IS NOT NULL;

CREATE TABLE stacks.entity_directory_identity_assertions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    decision_id uuid NOT NULL REFERENCES stacks.resolution_decisions(id),
    entity_id uuid NOT NULL REFERENCES stacks.entities(id),
    lookup_attempt_id uuid NOT NULL REFERENCES stacks.directory_lookup_attempts(id),
    snapshot_id uuid NOT NULL REFERENCES stacks.directory_profile_snapshots(id),
    provider text NOT NULL CHECK (provider IN ('google_people')),
    provider_subject_id text NOT NULL CHECK (btrim(provider_subject_id) <> ''),
    recorded_at timestamptz NOT NULL,
    UNIQUE (decision_id, provider, provider_subject_id)
);

CREATE INDEX entity_directory_identity_assertions_entity
    ON stacks.entity_directory_identity_assertions (entity_id);

CREATE INDEX entity_directory_identity_assertions_lookup_attempt
    ON stacks.entity_directory_identity_assertions (lookup_attempt_id);

CREATE INDEX entity_directory_identity_assertions_snapshot
    ON stacks.entity_directory_identity_assertions (snapshot_id);

CREATE INDEX entity_directory_identity_assertions_effective_identity
    ON stacks.entity_directory_identity_assertions (
        provider,
        provider_subject_id,
        entity_id,
        decision_id
    );

-- +goose StatementBegin
CREATE FUNCTION stacks.validate_effective_directory_identity(
    checked_provider text,
    checked_subject text
)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF (
        SELECT count(DISTINCT assertion.entity_id)
        FROM stacks.entity_directory_identity_assertions AS assertion
        JOIN stacks.resolution_decisions AS decision
          ON decision.id = assertion.decision_id
        WHERE assertion.provider = checked_provider
          AND assertion.provider_subject_id = checked_subject
          AND decision.superseded_by_id IS NULL
          AND decision.outcome IN ('accepted', 'created')
          AND decision.currently_admissible
    ) > 1 THEN
        RAISE EXCEPTION 'directory identity has conflicting effective entities';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION stacks.require_effective_directory_identity_for_assertion()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        PERFORM stacks.validate_effective_directory_identity(
            OLD.provider,
            OLD.provider_subject_id
        );
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        PERFORM stacks.validate_effective_directory_identity(
            NEW.provider,
            NEW.provider_subject_id
        );
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION stacks.require_effective_directory_identity_for_decision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    checked_identity record;
BEGIN
    IF TG_OP = 'DELETE' THEN
        FOR checked_identity IN
            SELECT DISTINCT assertion.provider, assertion.provider_subject_id
            FROM stacks.entity_directory_identity_assertions AS assertion
            WHERE assertion.decision_id = OLD.id
            ORDER BY assertion.provider, assertion.provider_subject_id
        LOOP
            PERFORM stacks.validate_effective_directory_identity(
                checked_identity.provider,
                checked_identity.provider_subject_id
            );
        END LOOP;
    ELSE
        FOR checked_identity IN
            SELECT DISTINCT assertion.provider, assertion.provider_subject_id
            FROM stacks.entity_directory_identity_assertions AS assertion
            WHERE assertion.decision_id IN (OLD.id, NEW.id)
            ORDER BY assertion.provider, assertion.provider_subject_id
        LOOP
            PERFORM stacks.validate_effective_directory_identity(
                checked_identity.provider,
                checked_identity.provider_subject_id
            );
        END LOOP;
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER entity_directory_identity_assertions_validate_effective
AFTER INSERT OR UPDATE OR DELETE ON stacks.entity_directory_identity_assertions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION stacks.require_effective_directory_identity_for_assertion();

CREATE CONSTRAINT TRIGGER resolution_decisions_validate_effective_directory_identity
AFTER UPDATE OR DELETE ON stacks.resolution_decisions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION stacks.require_effective_directory_identity_for_decision();
