# Misty environments

Misty has one canonical API `Dockerfile` and two Compose files:

- `compose.yml`, the server stack anyone runs: four env files under `~/.misty/server/prod/` and the setup
  container in [`setup/`](setup/). [docs/self-hosting.md](https://github.com/misty-org/misty/blob/main/docs/self-hosting.md) is the
  walkthrough and lists every value.
- `compose.dev.yml` for developing Misty, driven by the development-only `misty` CLI.

Both stacks run PostgreSQL, apply the same versioned SQL migrations, fix
application-role permissions, and then start the API. The migration and
permission containers are one-shot setup jobs: exit code 0 means they succeeded.
The API always listens on port 8080 inside its container.

## First-time development setup

Use the [server setup guide](../README.md#get-started). The CLI owns environment initialization, validation, Cloudflare provisioning, startup, and Worker deployment. The server is part of the Misty checkout under `server/`.

`misty setup server` creates private `~/.misty/server/dev/` files without replacing existing values. `misty env describe` lists variables and their files; `misty env set dev NAME` reads a value from stdin and writes it to the registered file. Use `misty env check dev` before startup and `misty doctor server` afterwards.

`misty setup cloudflare` previews infrastructure changes; `--apply` provisions the selected tunnel and DNS route. `misty server deploy` deploys the Worker separately. Cloudflare account IDs and hostnames are explicit configuration; there are no implicit Misty account defaults in the development stack.

The CLI passes every scoped environment file to Compose. A bare Compose invocation does not load the same configuration. Keep generated keys stable when retaining database volumes.

Do not recreate a service with only a subset of those files loaded for Compose interpolation. Explicit `environment` entries override service `env_file` values, including when interpolation supplies an empty string. For example, omitting `integrations/composio.env` can erase the running API's Composio key while its ordinary HTTP health check still passes. Use the CLI's environment loader and verify app tools after changes to it.

## Development

### AI models

Models run on each person's desktop, on the device's own keys
([docs/byok.md](https://github.com/misty-org/misty/blob/main/docs/byok.md)). Web search runs there too, on a search key the person keeps
on that device. The development stack has no agent runtime, no model keys and no search key
either: runs go to a desktop signed in to it, as they do in production. The private
`~/.misty/server/dev/integrations/ai.env` holds only the settings the server itself uses:

| Name | Value |
| --- | --- |
| `MISTY_RECALL`, `MISTY_RECALL_BACKLOG_CAP` | Recall indexing |

Then run `misty server up` to apply the configuration.

```sh
misty env check dev
misty server up
misty server deploy
misty doctor server
misty doctor cloudflare
```

The development stack contains the API, its database and the configured Cloudflare
tunnel. The collaboration Worker deploys separately
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

## Server stack

`compose.yml` has no profiles and nothing optional. Its tools image
([`setup/Dockerfile`](setup/Dockerfile)) runs every service that is not PostgreSQL or the API:

| Service | Role |
| --- | --- |
| `setup` | One-shot `misty-setup init`: generates missing secrets into the `secrets` volume, refuses to start when the database holds data but its secrets are gone, provisions Cloudflare ([docs/cloudflare.md](https://github.com/misty-org/misty/blob/main/docs/cloudflare.md)) |
| `postgres` | PostgreSQL with pgvector; its superuser password comes from the secrets volume |
| `migrate` | One-shot `misty-setup migrate`: syncs the API role's password, applies migrations, grants the role its tables |
| `api` | The Go API, published only on `127.0.0.1:8081`; it loads the secrets volume's `server.env` when it starts |
| `cloudflared` | The Tunnel connector, with the token setup saved |
| `backups` | `misty-setup backup`: age-encrypted `pg_dump` to the locked R2 backup bucket every night |
| `alerts` | `misty-setup watch`: records an open alert in the database when the server is down or failing, and clears it when it recovers |
| `console` | The operator console on `127.0.0.1:7070` |

Every service reads the four env files; only `setup` keeps `CLOUDFLARE_API_TOKEN`. The secrets
volume holds `server.env` (what the API and console read) and separate files for what the API must
never see: the database superuser password, the tunnel token, and the backup key and
credentials. There is no agent runtime and no workflow database: agents run on the desktop
([docs/agents/runtime.md](https://github.com/misty-org/misty/blob/main/docs/agents/runtime.md)).

### Alerts

`alerts` (`setup/watch.go`) checks once a minute, from inside the stack:

| Check | Problem |
| --- | --- |
| `GET http://api:8080/health` | No answer, or anything but 200 (a critical check failed; the alert names it) |
| `GET /metrics` with `MISTY_METRICS_TOKEN` | 5% or more of the requests since the last check failed with a 5xx, out of at least 20 |
| Free space on the `pgdata` volume | Under 10% |
| The newest object in the backup bucket (hourly) | Older than 26 hours, or none 26 hours after the service started |

A problem seen on two checks in a row becomes a row in `server_alerts` (written as the API's
restricted role in service mode, so `alerts` is on the `database` network too); the row is deleted
when the problem clears, and a failed write is tried again on the next check. The server sends no
email: the console's Overview and the owner's app (`GET /v1/server/alerts`) show open alerts. On
start it reads the open rows back, so one that cleared during a restart still closes. It can't see
the host itself go down, or record anything while the database is down; an outside check of `https://MISTY_HOSTNAME/health` covers that. The
Prometheus rules in [`observability/`](observability/) are for operators who run Prometheus.

`setup/stack-test.sh` runs the whole stack against the fake Cloudflare API in
`setup/fakecloudflare/`, on its own Compose project and free ports, with a throwaway CA as the
only one the containers trust. It checks health, first-account registration, that a second start
keeps every secret and uploads nothing, a backup and its restore, and that lost secrets over
existing data stop the stack, then removes everything it made. `go test ./deploy/setup/...` covers
the same logic without Docker.

## Desktop application

Misty ships only as a native desktop application. Build its renderer with
`npm run build:desktop` and package it through the desktop release workflow.
The `dist/` directory is an input to Tauri packaging, not a hosted application.
Set `MISTY_PUBLIC_API_URL` to the server's `https://MISTY_HOSTNAME/v1`.
