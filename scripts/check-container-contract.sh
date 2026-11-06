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
[ -f compose.yml ] ||
  fail "the server stack's Compose file is missing"
[ ! -e compose.prod.yml ] ||
  fail "compose.yml is the only server stack; compose.prod.yml must not return"
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
[ ! -e services/journal-collab/Dockerfile.dev ] ||
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
if grep -q 'trycloudflare.com' services/journal-collab/docker/cloudflare-deploy.sh; then
  fail "the development Worker callback must not depend on an ephemeral quick tunnel"
fi
cloudflare_env_consumers="$(grep -c '/integrations/cloudflare.env$' compose.dev.yml)"
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
if grep -Eq '^  agent-runtime:|MISTY_AGENT_RUNTIME_URL|world-postgres' compose.dev.yml; then
  fail "development runs no agent runtime or workflow database: agents run on the desktop"
fi
[ ! -e scripts/docker/postgres-workflow-database.sh ] ||
  fail "the workflow database setup must not return"
grep -q 'COPY --from=api /app/migrations' scripts/docker/setup.Dockerfile ||
  fail "setup must copy canonical API migrations"

# The server stack: nothing optional, everything from four env files and the
# setup container's secrets volume (docs/self-hosting.md).
if grep -Eq '^ *profiles:|COMPOSE_PROFILES' compose.yml; then
  fail "the server stack has no optional profiles"
fi
if grep -Eq '^  (agent-runtime|agent-runtime-setup|collab|database-permissions):' compose.yml; then
  fail "the server stack runs no agent runtime, workflow database or local collaboration Worker"
fi
for service in setup postgres migrate api cloudflared backups console; do
  grep -q "^  $service:" compose.yml || fail "the server stack is missing its $service service"
done
for file in misty cloudflare composio google; do
  grep -q "/$file.env" compose.yml || fail "the server stack must read ~/.misty/server/prod/$file.env"
done
! grep -q '/search.env' compose.yml ||
  fail "the server holds no search key: web search runs on each device"
grep -q 'service_completed_successfully' compose.yml ||
  fail "the server stack must wait for its setup jobs"
if git ls-files -- '*.env' '*.env.*' '.env*' 2>/dev/null | grep -q .; then
  fail "no env file is ever committed, not even an example"
fi
for compose_file in compose.dev.yml compose.yml; do
  if grep -qi 'activepieces' "$compose_file"; then
    fail "$compose_file must not deploy the retired automation service"
  fi
done
role_password_consumers="$(grep -c 'MISTY_APP_DB_PASSWORD:' compose.dev.yml)"
[ "$role_password_consumers" -ge 2 ] ||
  fail "compose.dev.yml must synchronize the application role password after migrations"
grep -q 'ALTER ROLE' scripts/docker/postgres-grant-app-role.sh ||
  fail "database permissions must rotate the persisted application role password"

grep -q '127.0.0.1:${MISTY_HOST_PORT:-8081}:8080' compose.yml ||
  fail "the server API must be loopback-only on host port 8081 by default"
grep -q '127.0.0.1:${MISTY_CONSOLE_PORT:-7070}:7070' compose.yml ||
  fail "the operator console must be loopback-only"

if grep -Eq '^  (stripe|tunnel|cloudflare-deploy|dev-init):' compose.yml; then
  fail "the server stack must not run development-only Stripe, tunnel, or Worker services"
fi

if grep -Eq 'ffmpeg|ffprobe|poppler|pdftoppm' Dockerfile; then
  fail "the API image must not contain server-side media tooling"
fi

echo "Development and the server stack use explicit Compose files and one canonical API image."
