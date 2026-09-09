#!/usr/bin/env bash
set -euo pipefail

# Destructive database suites always run in a disposable, loopback-only database.
# No developer database, local .env password, or existing Docker volume is used.
misty_test_root="$(cd "$(dirname "$0")/.." && pwd)"
misty_test_container="misty-automation-test-$$"
cleanup() { docker rm -fv "$misty_test_container" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM
docker run -d --name "$misty_test_container" \
  -e POSTGRES_PASSWORD=misty-isolated-test -e POSTGRES_DB=misty_automation_test \
  --tmpfs /var/lib/postgresql/data:rw,size=1536m \
  -p 127.0.0.1::5432 pgvector/pgvector:pg16 >/dev/null
for attempt in $(seq 1 60); do
  if docker exec "$misty_test_container" pg_isready -U postgres -d misty_automation_test >/dev/null 2>&1; then break; fi
  if [ "$attempt" = 60 ]; then echo "Disposable test database did not become ready." >&2; exit 1; fi
  sleep 1
done
misty_test_port="$(docker port "$misty_test_container" 5432/tcp | sed 's/.*://')"
export TEST_DB_HOST=127.0.0.1 TEST_DB_PORT="$misty_test_port"
export TEST_DB_USER=postgres TEST_DB_PASSWORD=misty-isolated-test
export TEST_DB_NAME=misty_automation_test TEST_DB_SSLMODE=disable
# The session contract suite separately verifies this ordinary RLS role.
export DB_USER=misty_app DB_PASSWORD=misty-isolated-runtime
export DB_HOST="$TEST_DB_HOST" DB_PORT="$TEST_DB_PORT" DB_NAME="$TEST_DB_NAME" DB_SSLMODE=disable
cd "$misty_test_root"
python3 - "$misty_test_container" <<'PY'
import pathlib, subprocess, sys
container = sys.argv[1]
def execute(sql):
    subprocess.run(["docker", "exec", "-i", container, "psql", "-q", "-v", "ON_ERROR_STOP=1",
                    "-U", "postgres", "-d", "misty_automation_test"], input=sql, text=True, check=True)
execute("CREATE ROLE misty_app LOGIN PASSWORD 'misty-isolated-runtime';\n"
        "CREATE TABLE goose_db_version(id BIGSERIAL PRIMARY KEY,version_id BIGINT NOT NULL,is_applied BOOLEAN NOT NULL,tstamp TIMESTAMP DEFAULT NOW());")
paths = sorted(pathlib.Path("internal/platform/postgres/migrations").glob("*.sql"))
versions = [int(path.name.split("_", 1)[0]) for path in paths]
if len(versions) != len(set(versions)):
    raise SystemExit("Duplicate migration versions: coordinate additive migration timestamps before testing.")
for path in paths:
    up = path.read_text().split("-- +goose Up", 1)[1].split("-- +goose Down", 1)[0]
    up = "\n".join(line for line in up.splitlines() if not line.startswith("-- +goose"))
    version = int(path.name.split("_", 1)[0])
    execute(f"BEGIN;\n{up}\nINSERT INTO goose_db_version(version_id,is_applied) VALUES({version},true);\nCOMMIT;")
print(f"Applied {len(paths)} migrations to a disposable database.")
PY
if [ "${MISTY_AUTOMATION_SKIP_INTERNAL:-0}" != "1" ]; then
  go test ./internal/...
fi
go test ./test/contract/postgres ./test/contract/http/api ./test/contract/http/app -count=1 "$@"
