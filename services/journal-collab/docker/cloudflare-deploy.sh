#!/bin/sh
set -eu

if [ -z "${CLOUDFLARE_API_TOKEN:-}" ]; then
  echo "CLOUDFLARE_API_TOKEN is required for the containerized Worker deployment." >&2
  echo "Add a Workers Scripts:Edit API token to ~/.misty/.env/server/cloudflare.env." >&2
  exit 1
fi

if [ -z "${JOURNAL_COLLAB_TICKET_PUBLIC_KEY:-}" ] ||
  [ -z "${JOURNAL_COLLAB_CONTROL_SECRET:-}" ] ||
  [ -z "${JOURNAL_COLLAB_PROJECTION_SECRET:-}" ]; then
  echo "The Worker secrets are missing from ~/.misty/.env/server/crypto/journal.env." >&2
  exit 1
fi

api_origin="${MISTY_DEV_API_ORIGIN:?run misty setup cloudflare}"
case "$api_origin" in
  https://*) ;;
  *)
    echo "Refusing non-HTTPS development API origin: $api_origin" >&2
    exit 1
    ;;
esac

api_base="${api_origin%/}/v1"
worker_name="${MISTY_CLOUDFLARE_WORKER_NAME:-misty-journal-collab-dev}"

# Only what the Worker needs: the private signing key never goes to Cloudflare.
secrets_file="$(mktemp)"
trap 'rm -f "$secrets_file"' EXIT
{
  printf 'JOURNAL_COLLAB_TICKET_PUBLIC_KEY=%s\n' "$JOURNAL_COLLAB_TICKET_PUBLIC_KEY"
  printf 'JOURNAL_COLLAB_CONTROL_SECRET=%s\n' "$JOURNAL_COLLAB_CONTROL_SECRET"
  printf 'JOURNAL_COLLAB_PROJECTION_SECRET=%s\n' "$JOURNAL_COLLAB_PROJECTION_SECRET"
} >"$secrets_file"

echo "Deploying $worker_name with callbacks to $api_base"
cd /app
npx wrangler deploy \
  --name "$worker_name" \
  --var "MISTY_INTERNAL_API_BASE:$api_base" \
  --secrets-file "$secrets_file"
