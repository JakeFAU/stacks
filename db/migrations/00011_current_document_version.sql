-- +goose Up
-- Immutable document versions remain available for audit, while each logical
-- source document explicitly identifies the completed version that represents
-- its current observed contents.
ALTER TABLE stacks.source_documents
    ADD COLUMN current_document_version_id uuid;

CREATE UNIQUE INDEX document_versions_source_document_identity
    ON stacks.document_versions (source_document_id, id);

ALTER TABLE stacks.source_documents
    ADD CONSTRAINT source_documents_current_version_belongs_to_source
    FOREIGN KEY (id, current_document_version_id)
    REFERENCES stacks.document_versions (source_document_id, id);

UPDATE stacks.source_documents AS source
SET current_document_version_id = current_version.document_version_id
FROM (
    SELECT DISTINCT ON (version.source_document_id)
        version.source_document_id,
        version.id AS document_version_id
    FROM stacks.document_versions AS version
    JOIN stacks.extraction_runs AS extraction_run
        ON extraction_run.document_version_id = version.id
    WHERE extraction_run.processing_status = 'complete'
      AND extraction_run.currently_admissible
    ORDER BY version.source_document_id,
             extraction_run.completed_at DESC,
             version.recorded_at DESC,
             version.id DESC
) AS current_version
WHERE source.id = current_version.source_document_id;
