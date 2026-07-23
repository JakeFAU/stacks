-- +goose Up
-- content_digest_v2 excludes the provider's optional revision marker. Existing
-- immutable version digests remain untouched; the first compatible sync may
-- attach this stable identity after matching the prior revision-inclusive
-- digest.
ALTER TABLE stacks.document_versions
    ADD COLUMN content_digest_v2 bytea;

ALTER TABLE stacks.document_versions
    ADD CONSTRAINT document_versions_content_digest_v2_sha256
        CHECK (content_digest_v2 IS NULL OR octet_length(content_digest_v2) = 32);

CREATE UNIQUE INDEX document_versions_one_content_digest_v2
    ON stacks.document_versions (source_document_id, content_digest_v2)
    WHERE content_digest_v2 IS NOT NULL;

-- Model-proposed email remains exact, cited audit provenance. It is separate
-- from normalized_email, which is reserved for a future deterministic trusted
-- provider boundary and is never populated by current extraction.
ALTER TABLE stacks.mentions
    ADD COLUMN proposed_email text NOT NULL DEFAULT '',
    ADD COLUMN proposed_email_evidence_span_id uuid REFERENCES stacks.evidence_spans(id);

ALTER TABLE stacks.mentions
    ADD CONSTRAINT mentions_proposed_email_evidence_pair CHECK (
        (proposed_email = '' AND proposed_email_evidence_span_id IS NULL) OR
        (btrim(proposed_email) <> '' AND proposed_email_evidence_span_id IS NOT NULL)
    );

-- Every currently admitted derived row predates the v4 extraction namespace,
-- v2 prompt/schema trust boundary, and v5 analysis policy. Preserve all audit
-- payloads and relationships, but require safe re-derivation before any row or
-- decision can participate in current projections. Alias assertions inherit
-- admission only from their owning decision.
UPDATE stacks.extraction_runs
SET currently_admissible = false
WHERE currently_admissible;

UPDATE stacks.mentions
SET currently_admissible = false
WHERE currently_admissible;

UPDATE stacks.resolution_decisions
SET currently_admissible = false
WHERE currently_admissible;

UPDATE stacks.observations
SET currently_admissible = false
WHERE currently_admissible;

UPDATE stacks.interaction_signals
SET currently_admissible = false
WHERE currently_admissible;

UPDATE stacks.analysis_runs
SET currently_admissible = false
WHERE currently_admissible;
