#!/usr/bin/env bash
# Replaces the production databases with an encrypted backup from R2, including
# billing when the backup holds it.
# Usage (through the CLI): misty server prod restore <stamp|latest> --identity <age key file> --yes
# On a fresh server run `misty server prod up` first so the roles exist; the
# stack is restarted afterwards, which applies any newer migrations.
set -euo pipefail
umask 077

backup="${1:?Name a backup timestamp or latest}"
identity="${2:?Pass the age identity file that decrypts backups}"
[ "${MISTY_RESTORE_CONFIRMED:-}" = yes ] || { echo "Restore replaces production data; rerun with --yes." >&2; exit 1; }
[ -r "$identity" ] || { echo "Cannot read age identity $identity." >&2; exit 1; }
: "${MISTY_BACKUP_BUCKET:?Set MISTY_BACKUP_BUCKET}"
: "${R2_ENDPOINT:?Set R2_ENDPOINT}"
: "${DB_MIGRATION_USER:?Set DB_MIGRATION_USER}" "${DB_NAME:?Set DB_NAME}"
command -v age >/dev/null || { echo "Install age (apt install age) to decrypt backups." >&2; exit 1; }

source "$(dirname "$0")/prod-backup-common.sh"
backup_credentials

if [ "$backup" = latest ]; then
  backup=$(rclone lsf --dirs-only "r2:$MISTY_BACKUP_BUCKET" | tr -d / | grep -E '^[0-9]{8}T[0-9]{6}Z$' | sort | tail -n 1)
  [ -n "$backup" ] || { echo "No backups found in r2:$MISTY_BACKUP_BUCKET." >&2; exit 1; }
fi
[[ "$backup" =~ ^[0-9]{8}T[0-9]{6}Z$ ]] || { echo "Backup names look like 20271003T020000Z." >&2; exit 1; }
dir=".misty/restore/$backup"
mkdir -p "$dir"
echo "Downloading $backup"
rclone copy "r2:$MISTY_BACKUP_BUCKET/$backup" "/work/$dir"
(cd "$dir" && sha256sum --check --quiet SHA256SUMS)
backup_database=$(sed -n 's/^misty_database=//p' "$dir/manifest.txt")
[ "$backup_database" = "$DB_NAME" ] || { echo "Backup holds database $backup_database, not $DB_NAME." >&2; exit 1; }
cat "$dir/manifest.txt"

echo "Stopping the API and agent runtime"
compose stop api agent-runtime
compose up --detach --wait postgres

restore() { # database owner
  compose exec -T postgres psql --username="$DB_MIGRATION_USER" --dbname=postgres --set=ON_ERROR_STOP=1 \
    --command="DROP DATABASE IF EXISTS \"$1\" WITH (FORCE)" --command="CREATE DATABASE \"$1\" OWNER \"$2\""
  age --decrypt --identity "$identity" "$dir/$1.dump.age" |
    compose exec -T postgres pg_restore --username="$DB_MIGRATION_USER" --dbname="$1" --exit-on-error
}
echo "Restoring $DB_NAME"
restore "$DB_NAME" "$DB_MIGRATION_USER"
echo "Restoring workflow"
restore workflow workflow

if [ -f "$dir/misty_billing.dump.age" ]; then
  billing=$(billing_postgres)
  if [ -z "$billing" ]; then
    echo "The backup includes billing, but the billing stack is not running. Start it with misty-billing's deploy/prod.sh up, then restore again." >&2
    exit 1
  fi
  echo "Restoring misty_billing"
  service=$(billing_service)
  [ -z "$service" ] || docker stop "$service" >/dev/null
  docker exec -i "$billing" psql --username=billing_owner --dbname=postgres --set=ON_ERROR_STOP=1 \
    --command="DROP DATABASE IF EXISTS misty_billing WITH (FORCE)" --command="CREATE DATABASE misty_billing OWNER billing_owner"
  age --decrypt --identity "$identity" "$dir/misty_billing.dump.age" |
    docker exec -i "$billing" pg_restore --username=billing_owner --dbname=misty_billing --exit-on-error
  [ -z "$service" ] || docker start "$service" >/dev/null
fi

echo "Restarting the stack (migrations and grants run again)"
compose up --detach
rm -rf -- "$dir"
echo "Restored $backup."
