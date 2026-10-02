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

# rclone runs in a container so the VPS needs only Docker. Credentials are
# passed by name from this process environment, never on the command line.
rclone() {
  export RCLONE_CONFIG_R2_TYPE=s3 RCLONE_CONFIG_R2_PROVIDER=Cloudflare
  export RCLONE_CONFIG_R2_ENDPOINT="$R2_ENDPOINT" RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true
  export RCLONE_CONFIG_R2_ACCESS_KEY_ID="$R2_ACCESS_KEY" RCLONE_CONFIG_R2_SECRET_ACCESS_KEY="$R2_SECRET_KEY"
  docker run --rm --user "$(id -u):$(id -g)" --volume "$PWD/.misty:/work/.misty" \
    --env RCLONE_CONFIG_R2_TYPE --env RCLONE_CONFIG_R2_PROVIDER --env RCLONE_CONFIG_R2_ENDPOINT \
    --env RCLONE_CONFIG_R2_NO_CHECK_BUCKET --env RCLONE_CONFIG_R2_ACCESS_KEY_ID --env RCLONE_CONFIG_R2_SECRET_ACCESS_KEY \
    "${MISTY_RCLONE_IMAGE:-rclone/rclone:1.71}" "$@"
}
