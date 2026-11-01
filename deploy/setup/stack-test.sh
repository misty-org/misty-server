#!/bin/sh
# Runs the whole server stack (server/compose.yml) against the fake Cloudflare
# API, on its own Compose project and free ports, and removes everything it
# made afterwards. It checks that the stack becomes healthy, that a second
# start keeps every secret and uploads nothing again, that a backup reaches the
# bucket, and that a lost secrets volume over existing data stops the stack.
#
#   server/deploy/setup/stack-test.sh
set -eu

server=$(cd "$(dirname "$0")/../.." && pwd)
project=${MISTY_STACK_TEST_PROJECT:-misty-stacktest}
work=$(mktemp -d)
free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'; }
fake_port=$(free_port)
s3_port=$(free_port)
MISTY_HOST_PORT=$(free_port)
MISTY_CONSOLE_PORT=$(free_port)
MISTY_ENV_DIR="$work/env"
export MISTY_HOST_PORT MISTY_CONSOLE_PORT MISTY_ENV_DIR

compose() { docker compose -p "$project" -f "$server/compose.yml" -f "$work/override.yml" "$@"; }

cleanup() {
	status=$?
	if [ "$status" -ne 0 ]; then
		compose logs --no-color --tail 60 setup migrate api >&2 2>/dev/null || true
	fi
	compose down --volumes --remove-orphans >/dev/null 2>&1 || true
	docker image rm "$project-tools:local" "$project-api:local" >/dev/null 2>&1 || true
	[ -n "${fake_pid:-}" ] && kill "$fake_pid" 2>/dev/null || true
	rm -rf "$work"
	exit "$status"
}
trap cleanup EXIT INT TERM

step() { printf '\n== %s\n' "$*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
state() { curl -fsS "http://127.0.0.1:$fake_port/_state"; }

# A throwaway CA is the only one the containers trust, so a call that would
# leave for a real HTTPS service (R2, Google) fails instead.
step "test CA and fake Cloudflare on :$fake_port (S3 on :$s3_port)"
mkdir -p "$work/ca"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=misty-stack-test-ca \
	-keyout "$work/ca/ca.key" -out "$work/ca/ca.pem" 2>/dev/null
openssl req -newkey rsa:2048 -nodes -subj /CN=host.docker.internal \
	-keyout "$work/ca/tls.key" -out "$work/ca/tls.csr" 2>/dev/null
printf 'subjectAltName=DNS:host.docker.internal\n' >"$work/ca/ext"
openssl x509 -req -days 1 -in "$work/ca/tls.csr" -CA "$work/ca/ca.pem" -CAkey "$work/ca/ca.key" \
	-CAcreateserial -extfile "$work/ca/ext" -out "$work/ca/tls.pem" 2>/dev/null
chmod 0644 "$work/ca/"*
(cd "$server" && go build -o "$work/fake-cloudflare" ./deploy/setup/fakecloudflare/cmd/fake-cloudflare)
"$work/fake-cloudflare" -addr "127.0.0.1:$fake_port" -tls-addr "127.0.0.1:$s3_port" \
	-tls-cert "$work/ca/tls.pem" -tls-key "$work/ca/tls.key" >"$work/fake.log" 2>&1 &
fake_pid=$!

mkdir -p "$MISTY_ENV_DIR"
fake="http://host.docker.internal:$fake_port"
printf 'MISTY_HOSTNAME=cloud.misty.example\n' >"$MISTY_ENV_DIR/misty.env"
cat >"$MISTY_ENV_DIR/cloudflare.env" <<EOF
CLOUDFLARE_API_TOKEN=fake-cloudflare-token
CLOUDFLARE_ACCOUNT_ID=0123456789abcdef0123456789abcdef
CLOUDFLARE_ZONE_ID=fedcba9876543210fedcba9876543210
MISTY_CLOUDFLARE_API_URL=$fake/client/v4
MISTY_R2_ENDPOINT_URL=https://host.docker.internal:$s3_port
EOF
printf 'MISTY_COMPOSIO_API_KEY=test-composio\n' >"$MISTY_ENV_DIR/composio.env"
printf 'GOOGLE_CLIENT_ID=test.apps.googleusercontent.com\nGOOGLE_CLIENT_SECRET=test\n' >"$MISTY_ENV_DIR/google.env"

# The tunnel connector would dial Cloudflare's real edge; here it only checks
# that setup left it a token.
cat >"$work/override.yml" <<EOF
x-test-ca: &test-ca
  environment:
    SSL_CERT_FILE: /run/misty-test/ca.pem
  volumes:
    - $work/ca:/run/misty-test:ro
services:
  setup: *test-ca
  migrate: *test-ca
  api: *test-ca
  backups: *test-ca
  console: *test-ca
  cloudflared:
    entrypoint: ["/bin/sh", "-ec", "test -s /run/misty/secrets/cloudflared/token; exec sleep infinity"]
    healthcheck: {disable: true}
EOF

wait_healthy() {
	i=0
	until curl -fsS "http://127.0.0.1:$MISTY_HOST_PORT/health" >"$work/health.json" 2>/dev/null; do
		i=$((i + 1))
		[ "$i" -gt 90 ] && fail "the API never answered /health"
		sleep 2
	done
	grep -q '"status":"ok"' "$work/health.json" || fail "health is not ok: $(cat "$work/health.json")"
}
secrets_digest() {
	compose run --rm --no-deps -T --entrypoint sh setup -c 'cat /run/misty/secrets/server.env /run/misty/secrets/postgres/superuser-password /run/misty/secrets/backup/identity' | shasum -a 256
}

step "first start"
compose up -d --build
wait_healthy
state >"$work/state1.json"
first=$(secrets_digest)
echo "healthy; $(cat "$work/state1.json")"

register() {
	curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$MISTY_HOST_PORT/v1/register" \
		-H 'Content-Type: application/json' -H 'X-Misty-CSRF: 1' -H 'Origin: tauri://localhost' \
		-d "{\"username\":\"$1\",\"email\":\"$1@misty.example\",\"password\":\"stack test password\"}"
}
step "the console answers on its loopback port"
i=0
until curl -fsS "http://127.0.0.1:$MISTY_CONSOLE_PORT/healthz" >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -gt 30 ] && fail "the console never answered"
	sleep 2
done
compose logs --no-color console | grep -q "http://127.0.0.1:$MISTY_CONSOLE_PORT/?token=" || fail "the console printed no sign-in link"

step "the first account registers; the next needs an invitation"
[ "$(register owner)" = 201 ] || fail "the first account could not register"
[ "$(register stranger)" = 403 ] || fail "a second account registered without an invitation"

step "second start keeps every secret"
compose up -d
wait_healthy
[ "$(secrets_digest)" = "$first" ] || fail "a second up changed the secrets"
state >"$work/state2.json"
grep -q '"uploads":1' "$work/state2.json" || fail "the Workers were uploaded again: $(cat "$work/state2.json")"
grep -q '"tunnels":1' "$work/state2.json" || fail "a second tunnel was made"

step "a backup reaches the bucket"
compose exec -T backups misty-setup backup --now
state | grep -q '"objects":1' || fail "no backup object in the bucket"
compose exec -T backups misty-setup backups | grep -q 'dump.age' || fail "the backup is not listed"

step "the backup restores"
compose stop api >/dev/null
compose exec -T backups misty-setup restore latest
compose up -d
wait_healthy
[ "$(register owner)" = 409 ] || fail "the restored database lost its account"

step "lost secrets over existing data stop the stack"
compose down
docker volume rm "${project}_secrets" >/dev/null
if compose up -d >"$work/up.log" 2>&1; then
	fail "the stack started without its secrets"
fi
compose logs --no-color setup 2>&1 | grep -q "secrets lost" || fail "setup did not explain the refusal"
[ -z "$(compose ps --status running -q api)" ] || fail "the API started anyway"
echo "refused: $(compose logs --no-color setup 2>&1 | grep 'secrets lost' | tail -1)"

step "passed"
