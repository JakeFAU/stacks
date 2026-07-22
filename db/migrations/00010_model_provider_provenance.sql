-- +goose Up
ALTER TABLE stacks.extraction_runs
    ADD COLUMN model_provider text,
    ADD COLUMN data_mode text;

ALTER TABLE stacks.analysis_runs
    ADD COLUMN model_provider text,
    ADD COLUMN data_mode text;

UPDATE stacks.extraction_runs
SET model_provider = 'bedrock',
    data_mode = 'legacy';

UPDATE stacks.analysis_runs
SET model_provider = 'bedrock',
    data_mode = 'legacy'
WHERE model_id IS NOT NULL;

ALTER TABLE stacks.extraction_runs
    ALTER COLUMN bedrock_region DROP NOT NULL;

ALTER TABLE stacks.extraction_runs
    ALTER COLUMN model_provider SET NOT NULL,
    ALTER COLUMN data_mode SET NOT NULL;

ALTER TABLE stacks.extraction_runs
    ADD CONSTRAINT extraction_runs_model_provider_bounded
        CHECK (model_provider IN ('bedrock', 'openai', 'anthropic')),
    ADD CONSTRAINT extraction_runs_data_mode_bounded
        CHECK (data_mode IN ('personal', 'restricted', 'legacy')),
    ADD CONSTRAINT extraction_runs_provider_region_consistent CHECK (
        (model_provider = 'bedrock'
            AND bedrock_region IS NOT NULL
            AND bedrock_region = btrim(bedrock_region)
            AND bedrock_region <> '') OR
        (model_provider IN ('openai', 'anthropic') AND bedrock_region IS NULL)
    );

ALTER TABLE stacks.analysis_runs
    ADD CONSTRAINT analysis_runs_model_provider_bounded
        CHECK (model_provider IS NULL OR model_provider IN ('bedrock', 'openai', 'anthropic')),
    ADD CONSTRAINT analysis_runs_data_mode_bounded
        CHECK (data_mode IS NULL OR data_mode IN ('personal', 'restricted', 'legacy')),
    ADD CONSTRAINT analysis_runs_provider_region_consistent CHECK (
        model_provider IS NULL OR
        (model_provider = 'bedrock'
            AND bedrock_region IS NOT NULL
            AND bedrock_region = btrim(bedrock_region)
            AND bedrock_region <> '') OR
        (model_provider IN ('openai', 'anthropic') AND bedrock_region IS NULL)
    ),
    ADD CONSTRAINT analysis_runs_completed_model_provenance CHECK (
        state <> 'complete' OR
        (
            model_provider IS NULL
            AND data_mode IS NULL
            AND bedrock_region IS NULL
            AND model_id IS NULL
            AND max_output_tokens IS NULL
        ) OR
        (
            model_provider IS NOT NULL
            AND data_mode IS NOT NULL
            AND model_id IS NOT NULL
            AND model_id = btrim(model_id)
            AND model_id <> ''
            AND max_output_tokens IS NOT NULL
            AND max_output_tokens > 0
            AND report_json IS NOT NULL
        )
    );
