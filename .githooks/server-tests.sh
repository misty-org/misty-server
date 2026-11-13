#!/usr/bin/env bash
# Runs the Go server suite against a disposable PostgreSQL container, exactly
# as production runs it (same pinned image); the dev database is untouched.
# Arguments replace the default ./... package list, e.g.
#   .githooks/server-tests.sh ./test/contract/postgres -run NativeNote
# With no arguments the suite runs in parallel: every job gets its own copy of
# the migrated database, and the slowest package's tests are split across jobs.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
image=pgvector/pgvector:pg16@sha256:1d533553fefe4f12e5d80c7b80622ba0c382abb5758856f52983d8789179f0fb
password=misty-test-password
# The data directory lives in memory: tests churn table files constantly, and
# Docker's disk is slow on macOS. Nothing here needs to survive the run.
container=$(docker run -d --rm -p 127.0.0.1::5432 --tmpfs /var/lib/postgresql/data:rw,size=2g \
  -e POSTGRES_USER=misty -e POSTGRES_PASSWORD=$password -e POSTGRES_DB=misty_test \
  "$image" -c checkpoint_timeout=30s -c fsync=off -c synchronous_commit=off -c full_page_writes=off)
trap 'docker rm -fv "$container" >/dev/null 2>&1 || true' EXIT
# Stopped at the commit limit: the container still has to go.
trap 'exit 143' TERM
port=$(docker port "$container" 5432/tcp | head -n 1 | sed 's/.*://')
waited=0
until docker exec "$container" pg_isready -U misty -d misty_test >/dev/null 2>&1; do
  # A container that exits (for example, Docker out of disk) never becomes ready.
  if [ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null)" != true ] || ((++waited > 60)); then
    echo "Test PostgreSQL did not start:" >&2
    docker logs --tail 20 "$container" >&2 2>&1 || true
    exit 1
  fi
  sleep 1
done

export DB_HOST=127.0.0.1 DB_PORT=$port DB_USER=misty DB_PASSWORD=$password DB_NAME=misty_test DB_SSLMODE=disable
export TEST_DB_HOST=127.0.0.1 TEST_DB_PORT=$port TEST_DB_USER=misty TEST_DB_PASSWORD=$password
export TEST_DB_NAME=misty_test TEST_DB_SSLMODE=disable
export MISTY_ENVIRONMENT=

bin="$PWD/.misty/bin"
[ -x "$bin/goose" ] || GOBIN="$bin" go install \
  -tags="no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb" \
  github.com/pressly/goose/v3/cmd/goose@v3.27.3
PATH="$bin:$PATH" ./scripts/goose.sh up >/dev/null
PGHOST=127.0.0.1 PGPORT=$port PGUSER=misty PGPASSWORD=$password PGDATABASE=misty_test \
  MISTY_APP_DB_USER=misty_app MISTY_APP_DB_PASSWORD=misty-app-test-password \
  ./scripts/docker/postgres-grant-app-role.sh >/dev/null
quiet() { grep -v -e '^ok ' -e 'no test files' "$1" || true; }

if [ $# -gt 0 ]; then
  log=$(mktemp)
  set +e
  go test -p 1 -timeout 30m -count=1 "$@" >"$log" 2>&1
  status=$?
  set -e
  quiet "$log"
  rm -f "$log"
  exit "$status"
fi

# One package holds most of the database tests (about a minute alone); its
# tests are dealt round-robin across shards. The other packages share the rest.
heavy=./test/contract/postgres
heavy_shards=${MISTY_GO_HEAVY_SHARDS:-4}
other_jobs=${MISTY_GO_OTHER_JOBS:-2}
heavy_tests=$(go test -list '^Test' "$heavy" | grep '^Test')
others=$(go list ./... | grep -v -x "$(go list "$heavy")")

# Tests reset their database between cases, so jobs never share one. A clone of
# the migrated database is a file copy: nothing is connected to the template.
jobs=$((heavy_shards + other_jobs))
for i in $(seq 1 "$jobs"); do
  docker exec "$container" psql -q -U misty -d postgres -v ON_ERROR_STOP=1 \
    -c "CREATE DATABASE misty_test_$i TEMPLATE misty_test" >/dev/null
done

logs=$(mktemp -d)
pids=()
job() { # number, then go test arguments
  local number=$1
  shift
  DB_NAME=misty_test_$number TEST_DB_NAME=misty_test_$number \
    go test -p 1 -timeout 30m -count=1 "$@" >"$logs/$number.log" 2>&1
}
for shard in $(seq 1 "$heavy_shards"); do
  names=$(echo "$heavy_tests" | awk -v n="$heavy_shards" -v s="$shard" '(NR - 1) % n == s - 1' | paste -sd '|' -)
  [ -n "$names" ] || continue
  job "$shard" "$heavy" -run "^($names)\$" &
  pids+=($!)
done
for slot in $(seq 1 "$other_jobs"); do
  packages=$(echo "$others" | awk -v n="$other_jobs" -v s="$slot" '(NR - 1) % n == s - 1')
  [ -n "$packages" ] || continue
  # shellcheck disable=SC2086 # one package per word
  job "$((heavy_shards + slot))" $packages &
  pids+=($!)
done

status=0
for pid in "${pids[@]}"; do
  wait "$pid" || status=1
done
for log in "$logs"/*.log; do quiet "$log"; done
rm -rf "$logs"
exit "$status"
