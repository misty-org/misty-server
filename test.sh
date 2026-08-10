#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

DEV_COMPOSE=(docker compose -f compose.dev.yml)
if [[ -f .env.dev ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env.dev
  set +a
  DEV_COMPOSE=(docker compose --env-file .env.dev -f compose.dev.yml)
fi

EXPLICIT_TEST_DB_HOST="${TEST_DB_HOST:-}"
EXPLICIT_TEST_DB_USER="${TEST_DB_USER:-}"
EXPLICIT_TEST_DB_SSLMODE="${TEST_DB_SSLMODE:-}"

export TEST_DB_HOST="${TEST_DB_HOST:-${DB_HOST:-}}"
export TEST_DB_PORT="${TEST_DB_PORT:-${DB_PORT:-5432}}"
export TEST_DB_USER="${TEST_DB_USER:-${DB_USER:-}}"
export TEST_DB_PASSWORD="${TEST_DB_PASSWORD:-${DB_PASSWORD:-}}"
export TEST_DB_SSLMODE="${TEST_DB_SSLMODE:-${DB_SSLMODE:-disable}}"

if [[ -z "${TEST_DB_NAME:-}" ]]; then
  if [[ -n "${DB_NAME:-}" ]]; then
    if [[ "$DB_NAME" == *test* ]]; then
      export TEST_DB_NAME="$DB_NAME"
    else
      export TEST_DB_NAME="${DB_NAME}_test"
    fi
  fi
fi

BOOTSTRAP_TEST_DB="${BOOTSTRAP_TEST_DB:-auto}"
SHOULD_BOOTSTRAP_TEST_DB="false"

if [[ "$BOOTSTRAP_TEST_DB" == "1" || "$BOOTSTRAP_TEST_DB" == "true" ]]; then
  SHOULD_BOOTSTRAP_TEST_DB="true"
elif [[ "$BOOTSTRAP_TEST_DB" == "auto" && -z "$EXPLICIT_TEST_DB_HOST" ]]; then
  case "${TEST_DB_HOST:-}" in
    ""|"localhost"|"127.0.0.1"|"::1")
      SHOULD_BOOTSTRAP_TEST_DB="true"
      ;;
  esac
fi

if [[ "$SHOULD_BOOTSTRAP_TEST_DB" == "true" ]]; then
  if ! command -v docker >/dev/null 2>&1; then
    echo "TEST_DB_HOST is not set and Docker is not available."
    echo "Set TEST_DB_* or DB_* for an existing Postgres instance, then rerun $0."
    exit 2
  fi

  export TEST_DB_HOST="${TEST_DB_HOST:-localhost}"
  case "$TEST_DB_HOST" in
    "localhost"|"::1")
      # The development Postgres port is intentionally IPv4 loopback-only.
      # Avoid macOS resolving localhost to ::1 for the bootstrapped container.
      export TEST_DB_HOST="127.0.0.1"
      ;;
  esac
  export TEST_DB_PORT="${TEST_DB_PORT:-5432}"
  export TEST_DB_USER="${TEST_DB_USER:-misty}"
  export TEST_DB_PASSWORD="${TEST_DB_PASSWORD:-misty}"

  # DB_USER is the application role and is deliberately unprivileged, so it can
  # neither recreate the test database nor TRUNCATE between tests. Bootstrap and
  # run as the migration role when one is configured.
  if [[ -z "$EXPLICIT_TEST_DB_USER" && -n "${DB_MIGRATION_USER:-}" ]]; then
    export TEST_DB_USER="$DB_MIGRATION_USER"
    export TEST_DB_PASSWORD="${DB_MIGRATION_PASSWORD:-$TEST_DB_PASSWORD}"
  fi
  ADMIN_DB_USER="$TEST_DB_USER"

  # The bootstrapped container has no TLS, so a production DB_SSLMODE inherited
  # from .env.dev would fail every connection.
  if [[ -z "$EXPLICIT_TEST_DB_SSLMODE" ]]; then
    export TEST_DB_SSLMODE="disable"
  fi
  export TEST_DB_NAME="${TEST_DB_NAME:-misty_server_test}"
  export TEST_DB_SSLMODE="${TEST_DB_SSLMODE:-disable}"

  export DB_USER="${DB_USER:-$TEST_DB_USER}"
  export DB_PASSWORD="${DB_PASSWORD:-$TEST_DB_PASSWORD}"
  export DB_NAME="${DB_NAME:-misty_server}"
  export DB_PORT="${DB_PORT:-$TEST_DB_PORT}"

  "${DEV_COMPOSE[@]}" up -d postgres

  until "${DEV_COMPOSE[@]}" exec -T postgres pg_isready -U "$ADMIN_DB_USER" -d "$DB_NAME" >/dev/null 2>&1; do
    sleep 1
  done

  TEST_DB_NAME_LOWER="$(printf '%s' "$TEST_DB_NAME" | tr '[:upper:]' '[:lower:]')"
  if [[ "$TEST_DB_NAME_LOWER" != *test* ]]; then
    echo "Refusing to recreate non-test database: $TEST_DB_NAME"
    exit 2
  fi
  case "$TEST_DB_NAME" in
    *[!a-zA-Z0-9_]*)
      echo "Refusing test database name with unsupported characters: $TEST_DB_NAME"
      exit 2
      ;;
  esac

  "${DEV_COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$ADMIN_DB_USER" -d postgres <<SQL
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = '${TEST_DB_NAME}' AND pid <> pg_backend_pid();
DROP DATABASE IF EXISTS "${TEST_DB_NAME}";
CREATE DATABASE "${TEST_DB_NAME}";
SQL

  for migration in internal/platform/postgres/migrations/*.sql; do
    awk '
      /^-- \+goose Up$/ { in_up = 1; next }
      /^-- \+goose Down$/ { in_up = 0 }
      in_up && /^-- \+goose/ { next }
      in_up { print }
    ' "$migration" | "${DEV_COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$ADMIN_DB_USER" -d "$TEST_DB_NAME"
  done

  # Migrations are applied with psql rather than goose, so record them in the
  # version table goose would have written. checkSchemaVersion reads it, and
  # without this a bootstrapped database looks unmigrated.
  {
    echo "CREATE TABLE IF NOT EXISTS goose_db_version (id SERIAL PRIMARY KEY, version_id BIGINT NOT NULL, is_applied BOOLEAN NOT NULL, tstamp TIMESTAMP NULL DEFAULT now());"
    echo "TRUNCATE goose_db_version RESTART IDENTITY;"
    echo "INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, true);"
    for migration in internal/platform/postgres/migrations/*.sql; do
      version="$(basename "$migration" | cut -d_ -f1)"
      echo "INSERT INTO goose_db_version (version_id, is_applied) VALUES (${version}, true);"
    done
  } | "${DEV_COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$ADMIN_DB_USER" -d "$TEST_DB_NAME" >/dev/null
fi

./scripts/check-go-file-sizes.sh
go test ./... -count=1 "$@"
