-- +goose Up
ALTER TABLE stacks.document_versions
    ADD COLUMN failure_code text,
    ADD COLUMN retry_count integer NOT NULL DEFAULT 0;

ALTER TABLE stacks.document_versions
    ADD CONSTRAINT document_versions_failure_code_bounded
        CHECK (failure_code IS NULL OR failure_code IN (
            'source_error',
            'invalid_source',
            'model_error',
            'invalid_output',
            'storage_error'
        )),
    ADD CONSTRAINT document_versions_retry_count_nonnegative
        CHECK (retry_count >= 0);
