#!/bin/sh
set -eu

if [ -z "${CLOUDFLARE_API_TOKEN:-}" ]; then
  echo "CLOUDFLARE_API_TOKEN is required for the containerized Worker deployment." >&2
  echo "Add a Workers Scripts:Edit API token to the root .env.dev file." >&2
  exit 1
fi

if [ ! -s /workspace/.dev.vars ]; then
  echo "The generated Worker secrets file /workspace/.dev.vars is missing." >&2
  exit 1
fi

if [ ! -s /run/misty/tunnel-url ]; then
  echo "The Cloudflare tunnel URL is not ready." >&2
  exit 1
fi

tunnel_url="$(tr -d '\r\n' < /run/misty/tunnel-url)"
case "$tunnel_url" in
  https://*.trycloudflare.com) ;;
  *)
    echo "Refusing unexpected tunnel URL: $tunnel_url" >&2
    exit 1
    ;;
esac

api_base="${tunnel_url%/}/api"
worker_name="${MISTY_CLOUDFLARE_WORKER_NAME:-misty-journal-collab-dev}"

echo "Deploying $worker_name with callbacks to $api_base"
cd /app
exec npx wrangler deploy \
  --name "$worker_name" \
  --var "MISTY_INTERNAL_API_BASE:$api_base" \
  --secrets-file /workspace/.dev.vars
