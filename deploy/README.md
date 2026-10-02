# Misty environments

Misty has one canonical API `Dockerfile` and two explicit Compose files:

- `compose.dev.yml` for local development.
- `compose.prod.yml` for production.

Both environments run PostgreSQL, apply the same versioned SQL migrations, fix
application-role permissions, and then start the API. The migration and
permission containers are one-shot setup jobs: exit code 0 means they succeeded.
The API always listens on port 8080 inside its container.

## First-time development setup

Use the [server setup guide](../README.md#get-started). The CLI owns environment initialization, validation, Cloudflare provisioning, startup, and Worker deployment. The server is part of the Misty checkout under `server/`.

`misty setup server` creates private `server/.env/dev/` files without replacing existing values. `misty env describe` lists variables and their files; `misty env set dev NAME` reads a value from stdin and writes it to the registered file. Use `misty env check dev` before startup and `misty doctor server` afterwards.

`misty setup cloudflare` previews infrastructure changes; `--apply` provisions the selected tunnel and DNS route. `misty server deploy` deploys the Worker separately. Cloudflare account IDs and hostnames are explicit configuration; there are no implicit Misty account defaults in the development stack.

The CLI passes every scoped environment file to Compose. A bare Compose invocation does not load the same configuration. Keep generated keys stable when retaining database volumes.

## Development

```sh
misty env check dev
misty server up
misty server deploy
misty doctor server
misty doctor cloudflare
```

The development stack contains the API, databases, agent runtime,
and the configured Cloudflare tunnel. The collaboration Worker deploys separately
through `misty server deploy`. Ordinary startup does not redeploy it.

| Service | Default local address | Port setting |
| --- | --- | --- |
| API | `127.0.0.1:8081` | `MISTY_HOST_PORT` |
| PostgreSQL | `127.0.0.1:5435` | `DB_PORT` |

Cloudflare setup configures your API hostname to route to `http://misty-api:8080`.

The website runs locally at `http://localhost:5174`; Vite forwards `/v1` to the
local API. External callbacks and the collaboration Worker use the public API.
Configure provider webhook destinations using your own origin.

`misty server up` builds changed images, starts containers, and returns after
readiness checks. `--no-build` reuses existing images. Progress is summarized;
bounded redacted build output lives under `.misty/logs/`. Use
`misty server logs SERVICE --tail 100 --follow` to investigate a service.
Database volumes survive `misty server down`; only explicit `--volumes` removes them.

## Production

Misty is hosted only: one VPS runs the whole stack from `compose.prod.yml`.

| Service | Role |
| --- | --- |
| `postgres` | One PostgreSQL server (pgvector) holding two databases: Misty's and the agent runtime's `workflow` database, each owned by its own role |
| `migrate`, `database-permissions` | One-shot jobs: apply migrations, grant the application role, create the workflow role and database |
| `agent-runtime-setup`, `agent-runtime` | Workflow world migrations, then the durable agent loop |
| `api` | The Go API, published only on `127.0.0.1:8081` |

The collaboration Worker stays on Cloudflare and deploys separately.

### Billing

The private billing service (`misty-org/misty-billing`) runs on the same VPS as
its own Compose project with its own PostgreSQL, listening on `127.0.0.1:8091`.
Publish it as `billing.mistysys.com` through the same production tunnel: in the
Cloudflare dashboard open the tunnel's public hostnames and add
`billing.mistysys.com` → `http://127.0.0.1:8091`. Later
`misty setup cloudflare --target prod` runs keep that extra route. Stripe sends
webhooks to `https://billing.mistysys.com/stripe/webhook`; the API calls
`MISTY_BILLING_URL=https://billing.mistysys.com/adapter`, signed with the
`MISTY_BILLING_SECRET` both services share. See that repository's README for
its variables and first-time setup.

### Releases

Images build on this computer; nothing builds on GitHub. From a clean,
pushed commit:

```sh
misty server release 0.1.0
```

It runs the secret scan and the server suites, builds the API and agent
runtime images for `linux/amd64` (Go cross-compiles natively; the agent runtime
builds under emulation), pushes them to GHCR, tags the commit `server-v0.1.0`,
and saves both digests in `.env/prod/runtime.env`. This computer needs
`docker login ghcr.io` with a token that can write packages.

The packages are private. Log the VPS in once with a classic personal access
token that has only `read:packages`: `docker login ghcr.io`. Roll back by
pasting a previous release's digests and running `up` again.

### Production environment

This computer is the source of truth for production configuration. Generate it
once; existing values are never overwritten:

```sh
misty env init prod      # server/.env/prod and misty-billing/.env/prod/billing.env
misty env status prod    # names still missing, values never shown
```

That generates database passwords, signing keys, the agent runtime secret, the
billing adapter secret (written to both repos so they match), device keys and
Journal keys. Fill in the rest with `misty env set prod NAME` (value from
stdin). Run `misty setup cloudflare --target prod` here too, so the tunnel
token lands in this copy. Keep an encrypted copy of both `.env/prod` folders
off this computer: losing the signing keys signs everyone out and breaks
Journal tickets.

Set `MISTY_DEPLOY_HOST=misty@your-vps` in `misty/cli/.env/common.env`, then:

```sh
misty server prod push     # copy both .env/prod folders to the VPS
misty server prod deploy   # push, then git pull and start billing and the server there
```

`push` validates the environment first, streams it over SSH, and backs up the
VPS's previous copy under `.env-backups/`; files that exist only on the VPS are
kept. `deploy` expects the public `misty` repository cloned in the SSH user's
home (override with `--dir`) and `misty` on the VPS's PATH. Billing's private
source never lives on the VPS: `deploy` sends only its committed deploy files
(`compose.prod.yml` and `deploy/`) to `~/misty-billing` (`--billing-dir`), and
the service runs from its GHCR image.

### Deploy

Populate the real private files under `.env/prod/` (see above). Besides the image digests,
production requires `AGENT_RUNTIME_DB_PASSWORD`,
`MISTY_AGENT_RUNTIME_CONTROL_SECRET` (`openssl rand -base64 32`), and the HTTP
billing adapter. The CLI validates file ownership, permissions, duplicate
names, placeholders, and required values.

```sh
misty env status prod
misty server prod check
misty server prod up
```

Production does not run a temporary tunnel or development Worker deployment.
The named Cloudflare Tunnel or reverse proxy publishes the API as
`https://api.mistysys.com/v1`. Check the API log for `SECURITY:` lines after
the first boot; a missing security setting logs a warning instead of failing.

### Backups

`misty server prod backup` dumps Misty's and the workflow database with
`pg_dump` (and billing's, when the misty-billing stack runs on the same host), streams each
dump straight into `age` (plaintext never touches the disk), and uploads the
ciphertext to the R2 bucket named by `MISTY_BACKUP_BUCKET` using the `R2_*`
credentials. Set `MISTY_BACKUP_AGE_RECIPIENT` to an age public key and keep the
matching private key **off** the VPS. The VPS needs Docker and `age`
(`apt install age`); `rclone` runs in a container. The three newest encrypted
copies also stay under `server/.misty/backups/`.

Run it nightly with the units in [`systemd/`](systemd/) (edit `User` and
`WorkingDirectory` first). Configure an R2 lifecycle rule on the bucket for
retention, for example deleting objects after 30 days.

Restore onto a running stack, or onto a fresh VPS after one `misty server prod up`:

```sh
misty server prod restore latest --identity ~/misty-backup.agekey --yes
```

Restore stops the API and agent runtime, replaces both databases, and starts
the stack again, which applies any newer migrations. Practise it on a spare
server: a backup that has never been restored is not yet a backup. Named Docker
volumes are persistent, but they are not backups.

## Browser app

The browser app is a separate static build. Build it from the Misty desktop
repository with the public API base baked in:

```sh
MISTY_PUBLIC_API_URL=https://api.mistysys.com/v1 npm run build:web
```

Serve that repository's `dist/` directory from the existing frontend server
with an SPA fallback to `index.html`, then route `app.mistysys.com` to that
server through a **named** Cloudflare Tunnel. A Tunnel maps the hostname to the
frontend origin; it does not perform Misty user-session routing. Do not expose
a Vite development server as the production origin.

The server environment must keep `MISTY_PUBLIC_API_URL` on the API host and
include both `https://mistysys.com` and `https://app.mistysys.com` in
`MISTY_ALLOWED_ORIGINS`. `MISTY_WEBSITE_URL`, password-reset URLs, and the
desktop-to-browser handoff redirect should point at `https://app.mistysys.com`;
the handoff start URL remains at `https://api.mistysys.com/v1/auth/handoff/start`.
The API's Secure HttpOnly cookie remains host-only on `api.mistysys.com` and is
sent with credentialed requests from the allowed Misty web origins.

Billing webhooks belong to the separately deployed billing service. The browser
server exposes no payment-provider webhook. Production must explicitly configure
the authenticated HTTP adapter; development may leave it disabled. See
[the public contract](https://github.com/misty-org/misty/wiki/Server-billing-adapter).

The production Journal Worker is deployed separately and points directly to:

```text
https://api.mistysys.com/v1
```

Run `misty server prod backup` before migrations. Library objects live in R2.
Preserve retired automation volumes separately until their data has been archived.

## Agent runtime rollout

The Go API owns data, authorization, the MCP catalog, tool execution, and the
managed-Misty migration. The agent runtime owns only the durable agent loop and
runs beside the API in `compose.prod.yml`, reaching it at `http://api:8080`.

Both containers read `MISTY_AGENT_RUNTIME_CONTROL_SECRET` from the production
environment. During rotation, put the old value in
`MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS`, run `misty server prod up`, then
remove the previous value after in-flight runs have drained.

The production Compose dependency chain applies migrations, application-role
permissions, the workflow database, and workflow migrations before the API
becomes healthy. For a rollout:

```sh
misty server prod check
misty server prod up

curl --fail https://api.mistysys.com/v1/health
```

The runtime only falls back to the legacy signed tool route when MCP discovery
is explicitly unavailable, and never replays a consequential call through both
transports.

Before opening traffic broadly, verify a read-only weather request and one
approved drawing write in a non-production Space. Production limits are split
by trust boundary: the public API edge has its own headroom, while `/mcp` separately limits each authenticated user/run/runtime
binding. The workflow also bounds transport retries, model steps, and repeated
identical failing tool calls.
