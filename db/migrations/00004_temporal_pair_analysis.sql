-- +goose Up
ALTER TABLE stacks.observations
    ADD COLUMN subject_mention_id uuid REFERENCES stacks.mentions(id),
    ADD COLUMN object_mention_id uuid REFERENCES stacks.mentions(id);

ALTER TABLE stacks.analysis_runs
    ADD COLUMN bedrock_region text,
    ADD COLUMN model_id text,
    ADD COLUMN max_output_tokens integer,
    ADD COLUMN report_json jsonb;

ALTER TABLE stacks.analysis_runs
    ADD CONSTRAINT analysis_runs_bedrock_region_present
        CHECK (bedrock_region IS NULL OR btrim(bedrock_region) <> ''),
    ADD CONSTRAINT analysis_runs_model_id_present
        CHECK (model_id IS NULL OR btrim(model_id) <> ''),
    ADD CONSTRAINT analysis_runs_max_output_tokens_positive
        CHECK (max_output_tokens IS NULL OR max_output_tokens > 0);

ALTER TABLE stacks.analysis_inputs
    DROP CONSTRAINT analysis_inputs_analysis_run_id_input_digest_key,
    ADD CONSTRAINT analysis_inputs_run_kind_input_unique
        UNIQUE (analysis_run_id, input_kind, input_id);
