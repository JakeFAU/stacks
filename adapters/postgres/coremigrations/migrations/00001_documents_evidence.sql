CREATE SCHEMA stacks_core;

CREATE TABLE stacks_core.source_documents (
    id text PRIMARY KEY,
    provider text NOT NULL CHECK (provider <> ''),
    provider_document_id text NOT NULL CHECK (provider_document_id <> ''),
    current_version_id text,
    created_at timestamptz(6) NOT NULL,
    UNIQUE (provider, provider_document_id)
);

CREATE TABLE stacks_core.document_versions (
    id text PRIMARY KEY,
    source_document_id text NOT NULL
        REFERENCES stacks_core.source_documents (id),
    digest_version text NOT NULL CHECK (digest_version <> ''),
    content_digest bytea NOT NULL
        CONSTRAINT document_versions_content_digest_check
        CHECK (octet_length(content_digest) = 32),
    title text NOT NULL CHECK (title <> ''),
    locator text NOT NULL CHECK (locator <> ''),
    provider_version text NOT NULL CHECK (provider_version <> ''),
    modified_at timestamptz(6) NOT NULL,
    source_time timestamptz(6),
    recorded_at timestamptz(6) NOT NULL,
    UNIQUE (source_document_id, digest_version, content_digest),
    UNIQUE (source_document_id, id)
);

ALTER TABLE stacks_core.source_documents
    ADD CONSTRAINT source_documents_current_version_fkey
    FOREIGN KEY (id, current_version_id)
    REFERENCES stacks_core.document_versions (source_document_id, id);

CREATE TABLE stacks_core.source_revision_observations (
    id text PRIMARY KEY,
    source_document_id text NOT NULL,
    document_version_id text NOT NULL,
    digest_version text NOT NULL CHECK (digest_version <> ''),
    digest bytea NOT NULL CHECK (octet_length(digest) = 32),
    provider_version text NOT NULL CHECK (provider_version <> ''),
    provider_revision text NOT NULL DEFAULT '',
    first_recorded_at timestamptz(6) NOT NULL,
    FOREIGN KEY (source_document_id, document_version_id)
        REFERENCES stacks_core.document_versions (source_document_id, id),
    UNIQUE (
        source_document_id,
        provider_version,
        provider_revision,
        document_version_id
    )
);

CREATE TABLE stacks_core.document_sections (
    document_version_id text NOT NULL
        REFERENCES stacks_core.document_versions (id),
    section_id text NOT NULL CHECK (section_id <> ''),
    title text NOT NULL CHECK (title <> ''),
    parent_id text NOT NULL DEFAULT '',
    path text[] NOT NULL,
    section_order integer NOT NULL CHECK (section_order >= 0),
    role text NOT NULL CHECK (role <> ''),
    content text NOT NULL,
    PRIMARY KEY (document_version_id, section_id),
    UNIQUE (document_version_id, section_order)
);

CREATE TABLE stacks_core.evidence_spans (
    id text PRIMARY KEY,
    document_version_id text NOT NULL,
    section_id text NOT NULL,
    digest_version text NOT NULL CHECK (digest_version <> ''),
    digest bytea NOT NULL
        CONSTRAINT evidence_spans_digest_check
        CHECK (octet_length(digest) = 32),
    start_offset bigint NOT NULL,
    end_offset bigint NOT NULL,
    quote text NOT NULL,
    recorded_at timestamptz(6) NOT NULL,
    CONSTRAINT evidence_spans_offsets_check
        CHECK (start_offset >= 0 AND end_offset > start_offset),
    FOREIGN KEY (document_version_id, section_id)
        REFERENCES stacks_core.document_sections (document_version_id, section_id)
);
