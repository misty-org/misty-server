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
[ ! -e apps/journal-collab/Dockerfile.dev ] ||
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
[ ! -e deploy/nginx.dev.conf ] ||
  fail "development must not retain the website Nginx tunnel configuration"
[ ! -e scripts/docker/nginx-dev-entrypoint.sh ] ||
  fail "development must not retain the website Nginx tunnel entrypoint"
grep -q 'MISTY_DEV_API_ORIGIN:?run misty setup cloudflare}/v1' compose.dev.yml ||
  fail "development must publish the canonical versioned API base"
grep -q 'MISTY_DEV_API_ORIGIN:?run misty setup cloudflare' compose.dev.yml ||
  fail "the development Worker must callback through the stable named API tunnel"
if grep -q 'trycloudflare.com' apps/journal-collab/docker/cloudflare-deploy.sh; then
  fail "the development Worker callback must not depend on an ephemeral quick tunnel"
fi
cloudflare_env_consumers="$(grep -c 'path: .env/dev/integrations/cloudflare.env' compose.dev.yml)"
[ "$cloudflare_env_consumers" -ge 2 ] ||
  fail "the development tunnel and Worker deploy must load the Cloudflare environment bundle"
grep -q '^  setup:' compose.dev.yml ||
  fail "development must have one startup service"
if grep -Eq '^  (dev-init|migrate|database-permissions|agent-runtime-setup|agent-runtime-postgres|cloudflare-deploy):' compose.dev.yml; then
  fail "development must consolidate startup jobs and PostgreSQL; deployment reuses setup"
fi
grep -q 'unset CLOUDFLARE_TUNNEL_TOKEN' compose.dev.yml ||
  fail "the development tunnel must remove the source credential before cloudflared logs its environment"

grep -q 'api: service:misty-api' compose.dev.yml ||
  fail "setup must obtain migrations from the same image as the API"
grep -q 'agent: service:agent-runtime' compose.dev.yml ||
  fail "setup must use the same workflow dependencies as the agent runtime"
grep -q 'COPY --from=api /app/migrations' scripts/docker/setup.Dockerfile ||
  fail "setup must copy canonical API migrations"

grep -q 'MISTY_API_IMAGE' compose.prod.yml ||
  fail "production must select an immutable MISTY_API_IMAGE"
production_image_consumers="$(grep -c '<<: \*api-image' compose.prod.yml)"
[ "$production_image_consumers" -ge 2 ] ||
  fail "production migrations and API must consume the same API image anchor"

for compose_file in compose.dev.yml compose.prod.yml self-host/compose.yml; do
  role_password_consumers="$(grep -c 'MISTY_APP_DB_PASSWORD:' "$compose_file")"
  [ "$role_password_consumers" -ge 2 ] ||
    fail "$compose_file must synchronize the application role password after migrations"
  if grep -qi 'activepieces' "$compose_file"; then
    fail "$compose_file must not deploy the retired automation service"
  fi

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
