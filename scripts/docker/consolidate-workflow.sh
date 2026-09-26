#!/bin/sh
# One-time development upgrade. Called before Compose removes orphan services.
set -eu
umask 077

container_for() {
  docker ps -aq --filter "label=com.docker.compose.project=${MISTY_COMPOSE_PROJECT:-misty-server}" \
    --filter "label=com.docker.compose.service=$1"
}

source_id=$(container_for agent-runtime-postgres)
[ -n "$source_id" ] || exit 0
marker=.misty/workflow-postgres-consolidated
[ ! -f "$marker" ] || exit 0
target_id=$(container_for postgres)
[ -n "$target_id" ] || { echo "Cannot find the main Postgres container for workflow migration." >&2; exit 1; }

target_sql() {
  docker exec -i "$target_id" sh -c 'exec psql -Xq -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' sh "$@"
}

docker start "$source_id" "$target_id" >/dev/null
for container_id in "$source_id" "$target_id"; do
  attempt=0
  until docker exec "$container_id" sh -c 'pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"' >/dev/null; do
    attempt=$((attempt + 1))
    [ "$attempt" -lt 30 ] || { echo "Postgres did not become ready for migration." >&2; exit 1; }
    sleep 1
  done
done

exists=$(printf "SELECT 1 FROM pg_database WHERE datname='workflow';\n" | target_sql -At)
if [ "$exists" = 1 ]; then
  echo "Both the legacy and shared workflow databases exist; refusing to overwrite either. Resolve the migration before starting the server." >&2
  exit 1
fi

backup_dir=".misty/backups/workflow-$(date -u +%Y%m%dT%H%M%SZ)-$$"
mkdir -p "$backup_dir"
running_consumers=""
for service in misty-api agent-runtime; do
  running_consumers="$running_consumers $(docker ps -q --filter "label=com.docker.compose.project=${MISTY_COMPOSE_PROJECT:-misty-server}" --filter "label=com.docker.compose.service=$service")"
done
created_database=false
finished=false
cleanup() {
  code=$?
  if [ "$finished" != true ]; then
    if [ "$created_database" = true ]; then
      printf 'DROP DATABASE workflow WITH (FORCE);\n' | target_sql >/dev/null || true
    fi
    for container_id in $running_consumers; do docker start "$container_id" >/dev/null || true; done
    echo "Workflow migration failed; the original database and backup were preserved." >&2
  fi
  exit "$code"
}
trap cleanup EXIT
for container_id in $running_consumers; do docker stop "$container_id" >/dev/null; done

echo "Backing up the workflow database to $backup_dir"
docker exec "$source_id" pg_dump -U workflow -d workflow --no-owner --no-acl > "$backup_dir/original.sql"
# pg_dump 18 emits one setting unavailable in PostgreSQL 16. The pinned
# PostgreSQL 16 client also predates psql's restrict/unrestrict meta-commands.
sed -e '/^SET transaction_timeout = 0;$/d' -e '/^\\restrict /d' -e '/^\\unrestrict /d' \
  "$backup_dir/original.sql" > "$backup_dir/postgres16.sql"

target_sql <<'SQL'
SELECT 'CREATE ROLE workflow LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS'
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'workflow')
\gexec
CREATE DATABASE workflow OWNER workflow;
SQL
created_database=true
{
  printf '\\connect workflow\nSET ROLE workflow;\n'
  cat "$backup_dir/postgres16.sql"
} | target_sql > "$backup_dir/restore.log" 2>&1

cat > "$backup_dir/counts.sql" <<'SQL'
SELECT format('SELECT %L || '':'' || count(*) FROM %I.%I;', schemaname || '.' || tablename, schemaname, tablename)
FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema') ORDER BY schemaname, tablename
\gexec
SQL
docker exec -i "$source_id" psql -XAt -v ON_ERROR_STOP=1 -U workflow -d workflow \
  < "$backup_dir/counts.sql" > "$backup_dir/source-counts.txt"
{ printf '\\connect workflow\n'; cat "$backup_dir/counts.sql"; } | target_sql -At \
  > "$backup_dir/target-counts.txt"
cmp "$backup_dir/source-counts.txt" "$backup_dir/target-counts.txt"
printf '%s\n' "$backup_dir" > "$marker"
finished=true
echo "Workflow database restored and table counts verified. Original volume retained."
