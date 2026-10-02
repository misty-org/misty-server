#!/bin/sh
# Creates the agent runtime's workflow role and database on the shared
# PostgreSQL server. Workflow ownership stays separate from the restricted Misty
# application role. Idempotent; rotates the role password to the configured one.
set -eu

: "${AGENT_RUNTIME_DB_PASSWORD:?Set AGENT_RUNTIME_DB_PASSWORD}"
psql --set=ON_ERROR_STOP=1 --set=workflow_password="$AGENT_RUNTIME_DB_PASSWORD" <<'SQL'
SELECT 'CREATE ROLE workflow LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS'
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'workflow')
\gexec
SELECT format('ALTER ROLE workflow WITH LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', :'workflow_password')
\gexec
SELECT 'CREATE DATABASE workflow OWNER workflow'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'workflow')
\gexec
REVOKE ALL ON DATABASE workflow FROM PUBLIC;
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', current_database())
\gexec
SQL

echo "Workflow database ready."
