-- +goose Up
-- The application role needs read-only access to Goose state so `stacks doctor`
-- can report pending migrations without using the migration-owner connection.
GRANT USAGE ON SCHEMA public TO stacks_app;
GRANT SELECT ON TABLE public.goose_db_version TO stacks_app;
