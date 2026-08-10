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
docker compose --env-file .env.dev -f compose.dev.yml up --build
```

Development adds the local image build, `.env.dev`, Stripe CLI forwarding,
the temporary Cloudflare tunnel, the development Worker deployment, and these
loopback-only host ports:

- API: `127.0.0.1:8081`
- PostgreSQL: `127.0.0.1:5435` by default

Use `MISTY_HOST_PORT` to change the API host port. `DB_PORT` changes the
development PostgreSQL host port.

The tunnel service logs the temporary public API base as
`https://<name>.trycloudflare.com/api` once it is ready. That HTTPS URL and
`http://127.0.0.1:8081/api` reach the same API container; the tunnel hostname
changes whenever the tunnel container is recreated.

## Production

Copy `deploy/production.env.example` to `.env.prod`, fill every placeholder,
make it owner-readable only, and set `MISTY_API_IMAGE` to the exact tested
image digest. Resolve the active `mattdev727` account in the production
database and set its immutable ID as `MISTY_OPERATOR_USER_ID` before running
the canonical Misty Space migration.

```sh
chmod 600 .env.prod

docker compose \
  --env-file .env.prod \
  -f compose.prod.yml \
  config --quiet

docker compose \
  --env-file .env.prod \
  -f compose.prod.yml \
  up -d
```

Production does not run the Stripe CLI, temporary tunnel, or development
Worker deployment. The API is available only at `127.0.0.1:8081`; Nginx
publishes it as `https://mistysys.com/api`.

The production Stripe Dashboard webhook must be:

```text
https://mistysys.com/api/stripe/webhook
```

The production Journal Worker is deployed separately and points directly to:

```text
https://mistysys.com/api
```

Run a PostgreSQL backup before migrations. A named Docker volume is persistent,
but it is not a backup.
