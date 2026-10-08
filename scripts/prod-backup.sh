#!/usr/bin/env bash
# Encrypted logical backup of the production databases, uploaded to R2: Misty's,
# the agent runtime's workflow database, and billing's when its stack runs here.
# Run through `misty server prod backup`, which loads the production
# environment. Plaintext dumps never touch the disk: pg_dump streams straight
# into age, and only ciphertext is kept locally and uploaded.
set -euo pipefail
umask 077

: "${MISTY_BACKUP_BUCKET:?Set MISTY_BACKUP_BUCKET}"
: "${MISTY_BACKUP_AGE_RECIPIENT:?Set MISTY_BACKUP_AGE_RECIPIENT (an age public key)}"
: "${R2_ENDPOINT:?Set R2_ENDPOINT}"
: "${DB_MIGRATION_USER:?Set DB_MIGRATION_USER}" "${DB_NAME:?Set DB_NAME}"
command -v age >/dev/null || { echo "Install age (apt install age) to encrypt backups." >&2; exit 1; }

source "$(dirname "$0")/prod-backup-common.sh"
backup_credentials

stamp=$(date -u +%Y%m%dT%H%M%SZ)
dir=".misty/backups/$stamp"
mkdir -p "$dir"
trap 'echo "Backup failed; partial files remain in $dir." >&2' ERR

for database in "$DB_NAME" workflow; do
  echo "Dumping $database"
  compose exec -T postgres pg_dump --username="$DB_MIGRATION_USER" --dbname="$database" --format=custom \
    | age --encrypt --recipient "$MISTY_BACKUP_AGE_RECIPIENT" --output "$dir/$database.dump.age"
done
billing=$(billing_postgres)
if [ -n "$billing" ]; then
  echo "Dumping misty_billing"
  docker exec "$billing" pg_dump --username=billing_owner --dbname=misty_billing --format=custom \
    | age --encrypt --recipient "$MISTY_BACKUP_AGE_RECIPIENT" --output "$dir/misty_billing.dump.age"
else
  echo "Billing stack is not running on this host; skipping misty_billing." >&2
fi
{
  echo "created_at=$stamp"
  echo "misty_database=$DB_NAME"
  echo "api_image=${MISTY_API_IMAGE:-}"
  echo "agent_runtime_image=${MISTY_AGENT_RUNTIME_IMAGE:-}"
  echo "billing_included=$([ -n "$billing" ] && echo yes || echo no)"
} > "$dir/manifest.txt"
(cd "$dir" && sha256sum ./*.age manifest.txt > SHA256SUMS)

echo "Uploading to r2:$MISTY_BACKUP_BUCKET/$stamp"
# --immutable refuses to replace an existing object; each run uses a new prefix.
rclone copy --immutable "/work/$dir" "r2:$MISTY_BACKUP_BUCKET/$stamp"

# Keep the three newest local copies; R2 lifecycle rules handle remote retention.
find .misty/backups -mindepth 1 -maxdepth 1 -type d -name '20*Z' | sort -r | tail -n +4 |
  while read -r old; do rm -rf -- "$old"; done
echo "Backup $stamp complete."
