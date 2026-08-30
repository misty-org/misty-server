# Misty environments

Misty has one canonical API `Dockerfile` and two explicit Compose files:

- `compose.dev.yml` for local development.
- `compose.prod.yml` for production.

Both environments run PostgreSQL, apply the same versioned SQL migrations, fix
application-role permissions, and then start the API. The migration and
permission containers are one-shot setup jobs: exit code 0 means they succeeded.
The API always listens on port 8080 inside its container.

## Development

```sh
misty env check dev
misty server up
```

Development adds the local image build, the scoped `.env/dev/` bundle, the API-only Cloudflare
tunnel, the development Worker deployment, the self-hosted Activepieces automation services, and
these loopback-only host ports:

- API: `127.0.0.1:8081`
- PostgreSQL: `127.0.0.1:5435` by default
- Activepieces: `127.0.0.1:8090` by default

Use `MISTY_HOST_PORT` to change the API host port. `DB_PORT` changes the development PostgreSQL
host port. `ACTIVEPIECES_HOST_PORT` changes the Activepieces host port.

Configure the API route on the named tunnel:

```text
dev-api.mistysys.com -> http://misty-api:8080
```

The API publishes Activepieces beneath `https://dev-api.mistysys.com/activepieces` for server-side
MCP calls, automation callbacks, and webhooks. Misty uses the container's private URL for account
and project management, and the
desktop app uses Misty's own editor rather than opening the Activepieces interface. This keeps
`dev.mistysys.com` available for the staging website and avoids a second development tunnel
hostname.

The website runs locally at `http://localhost:5174`. Vite forwards its `/v1`
requests to the Go API at `http://127.0.0.1:8081`, so browser development does
not traverse Cloudflare. The API tunnel remains available for external
callbacks, the Stripe Dashboard webhook destination, and the development
Worker. `STRIPE_WEBHOOK_PATH` may contain that destination's complete URL; the
server mounts the URL's path and verifies deliveries with the corresponding
Dashboard signing secret.

`misty server up` is a complete development redeploy: it rebuilds buildable
images, force-recreates every Compose container, removes orphaned containers,
and publishes the collaboration Worker. Detached startup returns only after
the Worker deploy completes successfully and the public API tunnel is ready.

## Production

Populate the real private files under `.env/prod/` and set `MISTY_API_IMAGE`
to the exact tested image digest. Resolve the active operator account in the
production database and set its immutable ID as `MISTY_OPERATOR_USER_ID`
before running the canonical Misty Space migration. The CLI validates file
ownership, permissions, duplicate names, placeholders, and required values.

```sh
misty env status prod
misty server prod check
misty server prod up
```

Production does not run the Stripe CLI, temporary tunnel, or development
Worker deployment. The API is available only at `127.0.0.1:8081`; the
production reverse proxy or named Cloudflare Tunnel publishes it as
`https://api.mistysys.com/v1`.

Activepieces Community Edition is available only at `127.0.0.1:8090` by default. Set
`ACTIVEPIECES_PUBLIC_URL` in `.env/prod/integrations/activepieces.env` to its trusted HTTPS origin,
for example `https://automations.mistysys.com`, and publish that origin to
`http://activepieces-app:80` (named tunnel) or `http://127.0.0.1:8090` (host reverse proxy). Keep the
URL free of a trailing slash because Misty derives the MCP endpoint by appending `/mcp`.

After the first start, opening Agents → Automations causes the Misty API to initialize the internal
Activepieces administrator, create an isolated project for the signed-in Misty user, and mint a
short-lived project token. There is no separate Activepieces sign-in or connection flow.
Activepieces Community Edition has no license fee, though its app, worker, Postgres, and Redis
consume server resources. Confirm the Misty host has spare capacity; current Activepieces guidance
sizes each worker at 0.5 vCPU / 1 GB and its recommended Postgres baseline at 2 vCPU / 4 GB.

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

The production Stripe Dashboard webhook must be:

```text
https://api.mistysys.com/v1/stripe/webhook
```

The production Journal Worker is deployed separately and points directly to:

```text
https://api.mistysys.com/v1
```

Run PostgreSQL backups before migrations. Back up `activepieces_pgdata`,
`activepieces_redisdata`, and `activepieces_cache` with the Misty data. Named Docker volumes are
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

Deploy the Vercel runtime from `agent-runtime/` with `vercel deploy --prod`.
Either side may be deployed first: the runtime only falls back to the legacy
signed tool route when MCP discovery is explicitly unavailable, and never
replays a consequential call through both transports.

Before opening traffic broadly, verify a read-only weather request and one
approved drawing write in a non-production Space. Production limits are split
by trust boundary: the public API edge has enough headroom for shared Vercel
egress, while `/mcp` separately limits each authenticated user/run/runtime
binding. The workflow also bounds transport retries, model steps, and repeated
identical failing tool calls.
