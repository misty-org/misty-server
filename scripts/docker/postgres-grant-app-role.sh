#!/bin/sh
set -eu

if [ -z "${MISTY_APP_DB_USER:-}" ]; then
  echo "MISTY_APP_DB_USER is required." >&2
  exit 1
fi

psql \
  --set=ON_ERROR_STOP=1 \
  --set=app_user="$MISTY_APP_DB_USER" <<'SQL'
SELECT format('GRANT USAGE ON SCHEMA public TO %I', :'app_user')
\gexec

SELECT format(
  'GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %I',
  :'app_user'
)
\gexec

SELECT format(
  'GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO %I',
  :'app_user'
)
\gexec
SQL

echo "Database permissions granted to the Misty application role."
