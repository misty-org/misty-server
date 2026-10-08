# Shared helpers for prod-backup.sh and prod-restore.sh. Sourced, not run.

compose() {
  docker compose --file compose.prod.yml "$@"
}

# The billing stack (misty-billing's compose.prod.yml) runs as its own Compose
# project on the same VPS. Find its PostgreSQL container by project label.
billing_postgres() {
  docker ps --quiet --filter label=com.docker.compose.project=misty-billing-production \
    --filter label=com.docker.compose.service=postgres
}

billing_service() {
  docker ps --all --quiet --filter label=com.docker.compose.project=misty-billing-production \
    --filter label=com.docker.compose.service=billing
}

# Backups must survive a compromise of the server that writes them. Use a
# separate bucket with an R2 bucket lock (retention) rule, and an API token
# scoped to that bucket alone: the API's own R2 key can then neither read nor
# delete them, and nothing on the server can remove a locked backup early.
backup_credentials() {
  if [ -n "${MISTY_BACKUP_R2_ACCESS_KEY:-}" ] && [ -n "${MISTY_BACKUP_R2_SECRET_KEY:-}" ]; then
    BACKUP_R2_ACCESS_KEY=$MISTY_BACKUP_R2_ACCESS_KEY BACKUP_R2_SECRET_KEY=$MISTY_BACKUP_R2_SECRET_KEY
  else
    : "${R2_ACCESS_KEY:?Set MISTY_BACKUP_R2_ACCESS_KEY and MISTY_BACKUP_R2_SECRET_KEY}" "${R2_SECRET_KEY:?Set MISTY_BACKUP_R2_SECRET_KEY}"
    echo "SECURITY: backups use the API's R2 key. Set MISTY_BACKUP_R2_ACCESS_KEY and MISTY_BACKUP_R2_SECRET_KEY to a token scoped to the backup bucket." >&2
    BACKUP_R2_ACCESS_KEY=$R2_ACCESS_KEY BACKUP_R2_SECRET_KEY=$R2_SECRET_KEY
  fi
  if [ "${MISTY_BACKUP_BUCKET:-}" = "${R2_BUCKET:-}" ]; then
    echo "SECURITY: MISTY_BACKUP_BUCKET is the API's own bucket; keep backups in a separate, locked bucket." >&2
  fi
}

# rclone runs in a container so the VPS needs only Docker. Credentials are
# passed by name from this process environment, never on the command line.
rclone() {
  export RCLONE_CONFIG_R2_TYPE=s3 RCLONE_CONFIG_R2_PROVIDER=Cloudflare
  export RCLONE_CONFIG_R2_ENDPOINT="$R2_ENDPOINT" RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true
  export RCLONE_CONFIG_R2_ACCESS_KEY_ID="$BACKUP_R2_ACCESS_KEY" RCLONE_CONFIG_R2_SECRET_ACCESS_KEY="$BACKUP_R2_SECRET_KEY"
  docker run --rm --user "$(id -u):$(id -g)" --volume "$PWD/.misty:/work/.misty" \
    --env RCLONE_CONFIG_R2_TYPE --env RCLONE_CONFIG_R2_PROVIDER --env RCLONE_CONFIG_R2_ENDPOINT \
    --env RCLONE_CONFIG_R2_NO_CHECK_BUCKET --env RCLONE_CONFIG_R2_ACCESS_KEY_ID --env RCLONE_CONFIG_R2_SECRET_ACCESS_KEY \
    "${MISTY_RCLONE_IMAGE:-rclone/rclone:1.71}" "$@"
}
