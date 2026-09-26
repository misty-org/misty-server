#!/bin/sh
set -eu

case "${1:-startup}" in
  deploy)
    exec /usr/local/bin/misty-cloudflare-deploy
    ;;
  startup) ;;
  *) echo "Usage: misty-setup [startup|deploy]" >&2; exit 2 ;;
esac

step=initialization
trap 'code=$?; if [ "$code" -ne 0 ]; then echo "Setup failed during: $step" >&2; fi' EXIT

step="Misty migrations"
echo "Setup: $step"
goose up

step="application permissions"
echo "Setup: $step"
/usr/local/bin/misty-grant-app-role

step="workflow database"
echo "Setup: $step"
: "${AGENT_RUNTIME_DB_PASSWORD:?Set AGENT_RUNTIME_DB_PASSWORD}"
# Keep workflow ownership separate from the restricted Misty application role.
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

step="workflow migrations"
echo "Setup: $step"
node /opt/agent-runtime/node_modules/@workflow/world-postgres/bin/setup.js

step="collaboration configuration"
echo "Setup: $step"
/usr/local/bin/misty-cloudflare-init
echo "Server setup complete."
