-- +goose Up
-- Rows admitted after migration 00007 may still have been produced by the
-- superseded list/fetch snapshot merge, which could pair a fetched undated
-- title with a stale listed meeting time. The extraction namespace was not
-- stored separately, so conservatively retire every derived row that exists at
-- this boundary. Payloads and relationships remain immutable audit history;
-- new rows retain the existing currently_admissible default of true.
UPDATE stacks.extraction_runs
SET currently_admissible = false
WHERE currently_admissible;

UPDATE stacks.mentions
SET currently_admissible = false
WHERE currently_admissible;

-- Alias assertions inherit admission from their owning decision, so no alias
-- payload is rewritten or deleted here.
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
