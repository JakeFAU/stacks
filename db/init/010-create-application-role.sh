#!/usr/bin/env bash
set -Eeuo pipefail

: "${STACKS_DB_APP_PASSWORD:?STACKS_DB_APP_PASSWORD is required}"

psql \
  --dbname "${POSTGRES_DB}" \
  --set ON_ERROR_STOP=1 \
  --username "${POSTGRES_USER}" <<'SQL'
\getenv app_password STACKS_DB_APP_PASSWORD

CREATE ROLE stacks_app
  LOGIN
  PASSWORD :'app_password'
  NOSUPERUSER
  NOCREATEDB
  NOCREATEROLE
  NOINHERIT
  NOREPLICATION;

REVOKE ALL ON DATABASE stacks FROM PUBLIC;
GRANT CONNECT ON DATABASE stacks TO stacks_app;

REVOKE ALL ON SCHEMA public FROM PUBLIC;

ALTER ROLE stacks_app IN DATABASE stacks
  SET search_path = stacks_core;
SQL
