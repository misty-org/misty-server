#!/bin/sh
set -eu

if [ -z "${CLOUDFLARE_API_TOKEN:-}" ]; then
  echo "CLOUDFLARE_API_TOKEN is required for the containerized Worker deployment." >&2
  echo "Add a Workers Scripts:Edit API token to .env/dev/integrations/cloudflare.env." >&2
  exit 1
fi

if [ ! -s /workspace/.dev.vars ]; then
  echo "The generated Worker secrets file /workspace/.dev.vars is missing." >&2
  exit 1
fi

api_origin="${MISTY_DEV_API_ORIGIN:-https://dev-api.mistysys.com}"
case "$api_origin" in
  https://*) ;;
  *)
    echo "Refusing non-HTTPS development API origin: $api_origin" >&2
    exit 1
    ;;
esac

api_base="${api_origin%/}/v1"
worker_name="${MISTY_CLOUDFLARE_WORKER_NAME:-misty-journal-collab-dev}"

echo "Deploying $worker_name with callbacks to $api_base"
cd /app
exec npx wrangler deploy \
  --name "$worker_name" \
  --var "MISTY_INTERNAL_API_BASE:$api_base" \
  --secrets-file /workspace/.dev.vars
