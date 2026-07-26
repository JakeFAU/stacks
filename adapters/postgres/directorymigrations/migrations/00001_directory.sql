CREATE SCHEMA stacks_directory;

CREATE TABLE stacks_directory.profiles (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    provider text NOT NULL CHECK (btrim(provider) <> ''),
    provider_subject_id text NOT NULL CHECK (btrim(provider_subject_id) <> ''),
    recorded_at timestamptz(6) NOT NULL,
    UNIQUE (provider, provider_subject_id)
);

CREATE TABLE stacks_directory.snapshots (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    profile_id text NOT NULL
        REFERENCES stacks_directory.profiles (id),
    source_type text NOT NULL CHECK (
        source_type IN ('domain_profile', 'domain_contact')
    ),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    observed_at timestamptz(6),
    recorded_at timestamptz(6) NOT NULL,
    digest bytea NOT NULL CHECK (octet_length(digest) = 32),
    UNIQUE (profile_id, digest)
);

CREATE TABLE stacks_directory.profile_emails (
    snapshot_id text NOT NULL
        REFERENCES stacks_directory.snapshots (id),
    normalized_email text NOT NULL CHECK (btrim(normalized_email) <> ''),
    is_primary boolean NOT NULL,
    email_order integer NOT NULL CHECK (email_order >= 0),
    PRIMARY KEY (snapshot_id, normalized_email),
    UNIQUE (snapshot_id, email_order)
);

CREATE INDEX profile_emails_exact_email
    ON stacks_directory.profile_emails (normalized_email, snapshot_id);

CREATE TABLE stacks_directory.lookup_attempts (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    mention_id text NOT NULL
        REFERENCES stacks_core.mentions (id),
    proposal_id text NOT NULL
        REFERENCES stacks_core.resolution_proposals (id),
    provider text NOT NULL CHECK (btrim(provider) <> ''),
    query_kind text NOT NULL CHECK (query_kind IN ('email', 'name')),
    email_evidence text NOT NULL CHECK (
        email_evidence IN (
            'source_bound',
            'citation_verified',
            'reviewer_supplied',
            'none'
        )
    ),
    query_digest bytea NOT NULL CHECK (octet_length(query_digest) = 32),
    policy_version text NOT NULL CHECK (btrim(policy_version) <> ''),
    outcome text NOT NULL CHECK (
        outcome IN (
            'matched',
            'no_match',
            'ambiguous',
            'review',
            'disabled',
            'not_configured',
            'unauthorized',
            'forbidden',
            'rate_limited',
            'unavailable',
            'invalid_response',
            'result_limit_exceeded'
        )
    ),
    attempt_count integer NOT NULL CHECK (attempt_count >= 0),
    retry_after timestamptz(6),
    recorded_at timestamptz(6) NOT NULL,
    snapshot_ids text[] NOT NULL DEFAULT '{}',
    digest bytea NOT NULL CHECK (octet_length(digest) = 32),
    UNIQUE (mention_id, digest),
    CONSTRAINT lookup_attempts_retry_check CHECK (
        (
            outcome IN ('rate_limited', 'unavailable')
            AND retry_after IS NOT NULL
            AND retry_after >= recorded_at
        )
        OR
        (
            outcome NOT IN ('rate_limited', 'unavailable')
            AND retry_after IS NULL
        )
    )
);

CREATE INDEX lookup_attempts_reuse
    ON stacks_directory.lookup_attempts (mention_id, outcome, recorded_at);

CREATE INDEX lookup_attempts_retry
    ON stacks_directory.lookup_attempts (mention_id, retry_after)
    WHERE retry_after IS NOT NULL;

CREATE TABLE stacks_directory.entity_links (
    id text PRIMARY KEY CHECK (btrim(id) <> ''),
    profile_id text NOT NULL
        REFERENCES stacks_directory.profiles (id),
    snapshot_id text NOT NULL
        REFERENCES stacks_directory.snapshots (id),
    lookup_attempt_id text NOT NULL
        REFERENCES stacks_directory.lookup_attempts (id),
    proposal_id text NOT NULL
        REFERENCES stacks_core.resolution_proposals (id),
    candidate_id text NOT NULL
        REFERENCES stacks_core.resolution_candidates (id),
    decision_id text
        REFERENCES stacks_core.resolution_decisions (id),
    entity_id text NOT NULL
        REFERENCES stacks_core.entities (id),
    recorded_at timestamptz(6) NOT NULL,
    UNIQUE (candidate_id),
    CONSTRAINT entity_links_decision_pair_check CHECK (
        decision_id IS NULL OR btrim(decision_id) <> ''
    )
);
