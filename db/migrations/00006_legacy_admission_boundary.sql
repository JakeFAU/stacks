-- +goose Up
-- Rows already present before this migration retain their complete audit
-- payload but cannot participate in current resolution or analysis. New rows
-- are admissible only because they cross the post-fix validation boundary.
ALTER TABLE stacks.extraction_runs
    ADD COLUMN currently_admissible boolean NOT NULL DEFAULT false;

ALTER TABLE stacks.mentions
    ADD COLUMN currently_admissible boolean NOT NULL DEFAULT false;

ALTER TABLE stacks.resolution_decisions
    ADD COLUMN currently_admissible boolean NOT NULL DEFAULT false;

ALTER TABLE stacks.observations
    ADD COLUMN currently_admissible boolean NOT NULL DEFAULT false;

ALTER TABLE stacks.interaction_signals
    ADD COLUMN currently_admissible boolean NOT NULL DEFAULT false;

ALTER TABLE stacks.analysis_runs
    ADD COLUMN currently_admissible boolean NOT NULL DEFAULT false;

ALTER TABLE stacks.extraction_runs
    ALTER COLUMN currently_admissible SET DEFAULT true;

ALTER TABLE stacks.mentions
    ALTER COLUMN currently_admissible SET DEFAULT true;

ALTER TABLE stacks.resolution_decisions
    ALTER COLUMN currently_admissible SET DEFAULT true;

ALTER TABLE stacks.observations
    ALTER COLUMN currently_admissible SET DEFAULT true;

ALTER TABLE stacks.interaction_signals
    ALTER COLUMN currently_admissible SET DEFAULT true;

ALTER TABLE stacks.analysis_runs
    ALTER COLUMN currently_admissible SET DEFAULT true;
