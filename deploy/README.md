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

Populate the real private files under `.env/prod/` and set `MISTY_API_IMAGE`
to the exact tested image digest. The CLI validates file
ownership, permissions, duplicate names, placeholders, and required values.

```sh
misty env status prod
misty server prod check
misty server prod up
```

Production does not run a temporary tunnel or development
Worker deployment. The API is available only at `127.0.0.1:8081`; the
production reverse proxy or named Cloudflare Tunnel publishes it as
`https://api.mistysys.com/v1`.

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
server exposes no payment-provider webhook. Self-hosted deployments leave the
optional billing adapter disabled; hosted deployments must explicitly configure
the authenticated HTTP adapter. See [the public contract](https://github.com/misty-org/misty/wiki/Server-billing-adapter).

The production Journal Worker is deployed separately and points directly to:

```text
https://api.mistysys.com/v1
```

Run PostgreSQL and Library backups before migrations. Preserve retired automation volumes separately until their data has been archived. Named Docker volumes are
persistent, but they are not backups.

## Agent runtime rollout

The Go API owns data, authorization, the MCP catalog, tool execution, and the
managed-Misty migration. Vercel owns only the durable agent loop. The browser
frontend is a separate static deployment.

Generate one current control secret with `openssl rand -base64 32`. Store the
exact same value as `MISTY_AGENT_RUNTIME_CONTROL_SECRET` in
`.env/prod/crypto/services.env` and the
Vercel runtime. During rotation, put the old value in
`MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS` on both systems, deploy both, then
remove the previous value after in-flight runs have drained.

On Vercel, set `MISTY_INTERNAL_API_BASE` to the same HTTPS API base used by
`MISTY_AGENT_RUNTIME_INTERNAL_API_URL` on Go. This can be the VPS reverse proxy,
for example `https://api.mistysys.com/v1`; it must reach the signed internal routes
and `/mcp`. Set `MISTY_AGENT_RUNTIME_URL` on Go to the actual Vercel deployment
URL. A hostname named `agents.mistysys.com` is optional and exists only if you
create and attach that custom domain.

The production Compose dependency chain applies migrations and application-role
permissions before the API becomes healthy. For a rollout:

```sh
misty server prod check
misty server prod up

curl --fail https://api.mistysys.com/v1/health
curl --fail https://replace-with-your-runtime.vercel.app/health
```

Deploy the Vercel runtime from `apps/agent-runtime/` with `vercel deploy --prod`.
Either side may be deployed first: the runtime only falls back to the legacy
signed tool route when MCP discovery is explicitly unavailable, and never
replays a consequential call through both transports.

Before opening traffic broadly, verify a read-only weather request and one
approved drawing write in a non-production Space. Production limits are split
by trust boundary: the public API edge has enough headroom for shared Vercel
egress, while `/mcp` separately limits each authenticated user/run/runtime
binding. The workflow also bounds transport retries, model steps, and repeated
identical failing tool calls.
