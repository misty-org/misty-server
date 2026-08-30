#!/bin/sh
set -eu

fail() {
  echo "Container contract: $1" >&2
  exit 1
}

[ -f Dockerfile ] || fail "the canonical root Dockerfile is missing"
[ ! -e Dockerfile.demo ] || fail "Dockerfile.demo must not reintroduce a second API image"
[ ! -e docker-compose.demo.yml ] || fail "demo mode must use the primary stack, not a separate Compose application"

[ -f compose.dev.yml ] ||
  fail "the self-contained development Compose file is missing"
[ -f compose.prod.yml ] ||
  fail "the self-contained production Compose file is missing"
[ ! -e docker-compose.yml ] ||
  fail "the legacy shared Compose file must not return"
[ ! -e docker-compose.override.yml ] ||
  fail "the legacy automatic development overlay must not return"
[ ! -e deploy/compose.production.yml ] ||
  fail "the legacy production overlay must not return"
[ ! -e deploy/compose.staging.yml ] ||
  fail "the legacy standalone staging stack must not diverge from the shared stack"
[ ! -e Dockerfile.cloudflared-dev ] ||
  fail "the development tunnel build must stay inside compose.dev.yml"
[ ! -e cloudflare/journal-collab/Dockerfile.dev ] ||
  fail "the development Worker build must stay inside compose.dev.yml"

grep -q 'dockerfile: Dockerfile' compose.dev.yml ||
  fail "development must build the canonical root Dockerfile"

grep -q '^  misty-api:' compose.dev.yml ||
  fail "development must expose the Go API as misty-api"
grep -q 'container_name: misty-api' compose.dev.yml ||
  fail "the development API container must use the stable misty-api name"
if grep -Eq '^  (api|api-backend):' compose.dev.yml; then
  fail "development must not retain the ambiguous api or api-backend service names"
fi
if grep -q '^  stripe:' compose.dev.yml; then
  fail "development must use the Dashboard webhook destination instead of duplicate Stripe CLI forwarding"
fi
[ ! -e scripts/docker/stripe-listen.sh ] ||
  fail "development must not retain the duplicate Stripe CLI forwarder"
if grep -q '^  misty-website:' compose.dev.yml; then
  fail "development must not run a website proxy container"
fi
if grep -q '^  activepieces-edge:' compose.dev.yml; then
  fail "development must publish Activepieces through the API instead of the staging hostname"
fi
grep -q 'ACTIVEPIECES_PUBLIC_URL:-https://dev-api.mistysys.com/activepieces' compose.dev.yml ||
  fail "development must publish Activepieces under the named API tunnel"
grep -q 'MISTY_ACTIVEPIECES_PROXY_URL: http://activepieces-app:80' compose.dev.yml ||
  fail "the development API must proxy the namespaced Activepieces routes"
[ ! -e deploy/nginx.dev.conf ] ||
  fail "development must not retain the website Nginx tunnel configuration"
[ ! -e scripts/docker/nginx-dev-entrypoint.sh ] ||
  fail "development must not retain the website Nginx tunnel entrypoint"
grep -q 'https://dev-api.mistysys.com}/v1' compose.dev.yml ||
  fail "development must publish the canonical versioned API base"
grep -q 'MISTY_DEV_API_ORIGIN:-https://dev-api.mistysys.com' compose.dev.yml ||
  fail "the development Worker must callback through the stable named API tunnel"
if grep -q 'trycloudflare.com' cloudflare/journal-collab/docker/cloudflare-deploy.sh; then
  fail "the development Worker callback must not depend on an ephemeral quick tunnel"
fi
cloudflare_env_consumers="$(grep -c 'path: .env/dev/integrations/cloudflare.env' compose.dev.yml)"
[ "$cloudflare_env_consumers" -ge 2 ] ||
  fail "the development tunnel and Worker deploy must load the Cloudflare environment bundle"
grep -q 'container_name: misty-cloudflare-deploy' compose.dev.yml ||
  fail "the development Worker deploy needs a stable container name so the CLI can wait for completion"
grep -q 'unset CLOUDFLARE_TUNNEL_TOKEN' compose.dev.yml ||
  fail "the development tunnel must remove the source credential before cloudflared logs its environment"

development_image_consumers="$(grep -c '<<: \*api-image' compose.dev.yml)"
[ "$development_image_consumers" -ge 2 ] ||
  fail "development migrations and API must consume the same API image anchor"

grep -q 'MISTY_API_IMAGE' compose.prod.yml ||
  fail "production must select an immutable MISTY_API_IMAGE"
production_image_consumers="$(grep -c '<<: \*api-image' compose.prod.yml)"
[ "$production_image_consumers" -ge 2 ] ||
  fail "production migrations and API must consume the same API image anchor"

for compose_file in compose.dev.yml compose.prod.yml self-host/compose.yml; do
  role_password_consumers="$(grep -c 'MISTY_APP_DB_PASSWORD:' "$compose_file")"
  [ "$role_password_consumers" -ge 2 ] ||
    fail "$compose_file must synchronize the application role password after migrations"
  grep -q '^  activepieces-app:' "$compose_file" ||
    fail "$compose_file must run the Activepieces application"
  grep -q '^  activepieces-worker:' "$compose_file" ||
    fail "$compose_file must run a separate Activepieces worker"
  grep -q '^  activepieces-postgres:' "$compose_file" ||
    fail "$compose_file must isolate the Activepieces database"
  grep -q '^  activepieces-redis:' "$compose_file" ||
    fail "$compose_file must provide the Activepieces queue"
  activepieces_image_consumers="$(grep -c 'ghcr.io/activepieces/activepieces:0.88.3@sha256:3080dd4c59b5e969589562c1f7d5913b71184790586d0c43b8ea6f8b723b2ddb' "$compose_file")"
  [ "$activepieces_image_consumers" -eq 2 ] ||
    fail "$compose_file must pin the same tested Activepieces image for app and worker"
  grep -q '127.0.0.1:${ACTIVEPIECES_HOST_PORT:-8090}:80' "$compose_file" ||
    fail "$compose_file must expose Activepieces on loopback only"
  grep -q 'MISTY_ACTIVEPIECES_MCP_URL:' "$compose_file" ||
    fail "$compose_file must give Misty the managed Activepieces MCP URL"
  if awk '
    /^  (agent-runtime):/ { in_runtime=1; next }
    /^  [A-Za-z0-9_-]+:/ { in_runtime=0 }
    in_runtime && /MISTY_ACTIVEPIECES_MCP_URL:/ { found=1 }
    END { exit found ? 0 : 1 }
  ' "$compose_file"; then
    fail "$compose_file must keep the Activepieces MCP URL in the Misty API, not the agent runtime"
  fi
  grep -q 'AP_TELEMETRY_ENABLED: "false"' "$compose_file" ||
    fail "$compose_file must disable Activepieces telemetry for the self-hosted MVP"
  grep -q '^  activepieces-database:' "$compose_file" ||
    fail "$compose_file must define an isolated Activepieces data network"
done
grep -q 'ALTER ROLE' scripts/docker/postgres-grant-app-role.sh ||
  fail "database permissions must rotate the persisted application role password"

grep -q '127.0.0.1:${MISTY_HOST_PORT:-8081}:8080' \
  compose.prod.yml ||
  fail "production API must be loopback-only on host port 8081 by default"

if grep -Eq '^  (stripe|tunnel|cloudflare-deploy|dev-init):' compose.prod.yml; then
  fail "production must not run development-only Stripe, tunnel, or Worker services"
fi

if grep -Eq 'ffmpeg|ffprobe|poppler|pdftoppm' Dockerfile; then
  fail "the API image must not contain server-side media tooling"
fi

echo "Development and production use explicit Compose files and one canonical API image."
