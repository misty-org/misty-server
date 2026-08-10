#!/bin/sh
set -eu

if [ -z "${MISTY_APP_DB_USER:-}" ] || [ -z "${MISTY_APP_DB_PASSWORD:-}" ]; then
  echo "MISTY_APP_DB_USER and MISTY_APP_DB_PASSWORD are required." >&2
  exit 1
fi

psql \
  --username "$POSTGRES_USER" \
  --dbname "$POSTGRES_DB" \
  --set=app_user="$MISTY_APP_DB_USER" \
  --set=app_password="$MISTY_APP_DB_PASSWORD" <<'SQL'
SELECT format(
  'CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS',
  :'app_user',
  :'app_password'
)
WHERE NOT EXISTS (
  SELECT 1 FROM pg_roles WHERE rolname = :'app_user'
)
\gexec

SELECT format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), :'app_user')
\gexec
SQL
