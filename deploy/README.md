# Misty environments

Misty has one canonical API `Dockerfile` and two Compose files:

- `compose.dev.yml` for local development.
- `compose.prod.yml` for production and staging, selected with separate environment files and Compose project names.

Both stacks run PostgreSQL, apply the same versioned SQL migrations, fix
application-role permissions, and then start the API. The migration and
permission containers are one-shot setup jobs: exit code 0 means they succeeded.
The API always listens on port 8080 inside its container.

## First-time development setup

Use the [server setup guide](../README.md#get-started). The CLI owns environment initialization, validation, Cloudflare provisioning, startup, and Worker deployment. The server is part of the Misty checkout under `server/`.

`misty setup server` creates private `server/.env/dev/` files without replacing existing values. `misty env describe` lists variables and their files; `misty env set dev NAME` reads a value from stdin and writes it to the registered file. Use `misty env check dev` before startup and `misty doctor server` afterwards.

`misty setup cloudflare` previews infrastructure changes; `--apply` provisions the selected tunnel and DNS route. `misty server deploy` deploys the Worker separately. Cloudflare account IDs and hostnames are explicit configuration; there are no implicit Misty account defaults in the development stack.

The CLI passes every scoped environment file to Compose. A bare Compose invocation does not load the same configuration. Keep generated keys stable when retaining database volumes.

Do not recreate a service with only a subset of those files loaded for Compose interpolation. Explicit `environment` entries override service `env_file` values, including when interpolation supplies an empty string. For example, omitting `integrations/ai.env` can erase the running API's gateway key while its ordinary HTTP health check still passes. Use the CLI's environment loader and verify provider readiness after changes to AI services.

## Development

### AI models

Models run on each person's own keys, sent by their device ([docs/byok.md](../../docs/byok.md)).
A development stack can also have server-side models for evals and for accounts whose devices
send no keys. Set them in the private `server/.env/dev/integrations/ai.env`:

```dotenv
# Any provider/model ID (docs/byok.md), and the key for its provider.
MISTY_AGENT_MODEL=anthropic/claude-haiku-5-5
ANTHROPIC_API_KEY=<your Anthropic API key>
# Web search for agents (serper.dev); without it agents have no web.search.
MISTY_SERPER_API_KEY=<your Serper API key>
```

Then run `misty server up` to apply the configuration. A device's own keys always take
precedence over the server's.

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

## Staging

Staging uses the production-shaped Compose stack with its own configuration,
Compose project, database and volumes. It does not run the development tunnel or
deploy the Journal collaboration Worker. This keeps load-test traffic private;
Journal collaboration can be added as a separate staging Worker when needed.

On the home server, install Docker Engine and the Compose v2 plugin, clone this
repository, install the Misty CLI, then initialize and start staging:

```sh
cargo install --path cli --locked
misty env init staging
misty env status staging
misty env check staging
misty server staging check
misty server staging up
```

Initialization creates private files under `server/.env/staging/`, generates
staging-only database and signing secrets, selects local filesystem Library
storage, and builds the API and agent-runtime images from the checked-out source.
Add AI provider credentials only when a test needs real model calls. Do not copy
production credentials or customer data into staging. The API is published only
on `127.0.0.1:8082`; PostgreSQL and the agent runtime have no host ports.

Keep the stack running on the home server. From the computer that runs k6, open
an SSH tunnel to that loopback port:

```sh
ssh -L 8082:127.0.0.1:8082 user@home-server
```

Then follow [`server/bench/README.md`](../bench/README.md) to run the smoke
scenario against `http://127.0.0.1:8082/v1`. Staging Compose commands are:

```sh
misty server staging status
misty server staging logs
misty server staging down
```

`down` preserves staging data. `misty server staging down --volumes` explicitly
deletes its database and Library data. The staging project name is fixed to
`misty-staging`, keeping its named volumes isolated from production.

## Production

Production is your own server running `compose.prod.yml`; [docs/self-hosting.md](../../docs/self-hosting.md)
is the walkthrough.

| Service | Role |
| --- | --- |
| `postgres` | One PostgreSQL server (pgvector) holding two databases: Misty's and the agent runtime's `workflow` database, each owned by its own role |
| `migrate`, `database-permissions` | One-shot jobs: apply migrations, grant the application role, create the workflow role and database |
| `agent-runtime-setup`, `agent-runtime` | Workflow world migrations, then the durable agent loop |
| `api` | The Go API, published only on `127.0.0.1:8081` |

The optional Cloudflare Workers deploy separately to your own account ([docs/cloudflare.md](../../docs/cloudflare.md)).

### Releases

Without `MISTY_API_IMAGE` and `MISTY_AGENT_RUNTIME_IMAGE`, `misty server prod up` builds both
images from the checkout. To build once and run the same images on several hosts, publish them
to a registry you control from a clean, pushed commit:

```sh
misty server release 0.1.0
```

It runs the secret scan and the server suites, builds the API and agent runtime images for
`linux/amd64`, pushes them to the registry, tags the commit `server-v0.1.0`, and saves both
digests in `.env/prod/runtime.env`. Roll back by pasting a previous release's digests and running
`up` again.

### Production environment

The computer you run the CLI on is the source of truth for production configuration. Generate it
once; existing values are never overwritten:

```sh
misty env init prod      # server/.env/prod
misty env status prod    # names still missing, values never shown
```

That generates database passwords, signing keys, the agent runtime secret, device keys and
Journal keys. Fill in the rest with `misty env set prod NAME` (value from stdin). Keep an
encrypted copy of `.env/prod` somewhere else: losing the signing keys signs everyone out and
breaks Journal tickets.

To run the stack on another machine, set `MISTY_DEPLOY_HOST=you@your-server` in
`misty/cli/.env/common.env`, then:

```sh
misty server prod push     # copy .env/prod to the server
misty server prod deploy   # push, then git pull and start the stack there
```

`push` validates the environment first, streams it over SSH, and backs up the server's previous
copy under `.env-backups/`; files that exist only on the server are kept. `deploy` expects this
repository cloned in the SSH user's home (override with `--dir`) and `misty` on the server's PATH.

### Deploy

```sh
misty env status prod
misty server prod check
misty server prod up
```

Production does not run a temporary tunnel or development Worker deployment. A reverse proxy or
a named Cloudflare Tunnel publishes the API at your `MISTY_PUBLIC_API_URL`. Check the API log for
`SECURITY:` lines after the first boot; a missing security setting logs a warning instead of
failing.

### Backups

`misty server prod backup` dumps Misty's and the workflow database with
`pg_dump`, streams each
dump straight into `age` (plaintext never touches the disk), and uploads the
ciphertext to the R2 bucket named by `MISTY_BACKUP_BUCKET`. Set
`MISTY_BACKUP_AGE_RECIPIENT` to an age public key and keep the matching private
key **off** the server. The server needs Docker and `age` (`apt install age`); `rclone`
runs in a container. The three newest encrypted copies also stay under
`server/.misty/backups/`.

Backups must stay recoverable if the server itself is compromised, so keep them out
of reach of anything running there:

- Use a bucket of their own, not `R2_BUCKET`.
- Create an R2 API token with Object Read & Write on that bucket only, and set
  it as `MISTY_BACKUP_R2_ACCESS_KEY` and `MISTY_BACKUP_R2_SECRET_KEY`. Without
  them the backup falls back to the API's `R2_*` key and logs a `SECURITY:` line.
- Add a bucket lock rule so no token can delete or overwrite a backup during
  its retention period, for example
  `npx wrangler r2 bucket lock add misty-backups --name nightly --retention-days 30`.
  Retention there replaces a lifecycle rule that deletes earlier.

Run it nightly with the units in [`systemd/`](systemd/) (edit `User` and
`WorkingDirectory` first).

Restore onto a running stack, or onto a fresh server after one `misty server prod up`:

```sh
misty server prod restore latest --identity ~/misty-backup.agekey --yes
```

Restore stops the API and agent runtime, replaces both databases, and starts
the stack again, which applies any newer migrations. Practise it on a spare
server: a backup that has never been restored is not yet a backup. Named Docker
volumes are persistent, but they are not backups.

## Desktop application

Misty ships only as a native desktop application. Build its renderer with
`npm run build:desktop` and package it through the desktop release workflow.
The `dist/` directory is an input to Tauri packaging, not a hosted application.
Set `MISTY_PUBLIC_API_URL` for the API and `MISTY_ALLOWED_ORIGINS` for the native
app origins (and any website you run for password resets).

Run `misty server prod backup` before migrations.

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

curl --fail https://misty.example.com/v1/health
```

The runtime only falls back to the legacy signed tool route when MCP discovery
is explicitly unavailable, and never replays a consequential call through both
transports.

Before opening traffic broadly, verify a read-only weather request and one
approved drawing write in a non-production Space. Production limits are split
by trust boundary: the public API edge has its own headroom, while `/mcp` separately limits each authenticated user/run/runtime
binding. The workflow also bounds transport retries, model steps, and repeated
identical failing tool calls.
