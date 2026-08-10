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

development_image_consumers="$(grep -c '<<: \*api-image' compose.dev.yml)"
[ "$development_image_consumers" -ge 2 ] ||
  fail "development migrations and API must consume the same API image anchor"

grep -q 'MISTY_API_IMAGE' compose.prod.yml ||
  fail "production must select an immutable MISTY_API_IMAGE"
production_image_consumers="$(grep -c '<<: \*api-image' compose.prod.yml)"
[ "$production_image_consumers" -ge 2 ] ||
  fail "production migrations and API must consume the same API image anchor"

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
