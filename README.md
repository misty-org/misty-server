# Misty Server

Misty's private backend repository contains independently deployed API,
payments, agent and collaboration applications. The API is being migrated from
Go to Hono/TypeScript in verified stages; the Go deployment remains authoritative
until route and job parity is complete. See [the migration ledger](docs/migration/README.md).

## Repository layout

- `cmd/` and `internal/` — Go API entrypoints and application packages.
- `apps/api/` — Hono API composition and domain modules.
- `apps/payments/` — isolated payments service (migration in progress).
- `packages/` — private shared backend infrastructure and subscription policy for API/operator validation.
- `internal/platform/postgres/migrations/` — existing PostgreSQL schema migrations.
- `apps/agent-runtime/` — durable AI workflow execution runtime.
- `apps/journal-collab/` — collaborative journal and drawing worker.
- `apps/self-host-collab/` — independently packaged self-hosted Yjs service.
- `deploy/` and `self-host/` — managed and self-hosted deployment assets.

## Local development

The new Hono API is developed independently on port 8082 while the existing API
remains available. `npm ci` and `npm run check` verify the TypeScript foundation;
`npm run dev:api` starts it with explicit `DB_*` environment configuration.
Its `/readyz` remains unavailable until migration parity has been verified.

Copy the development environment templates described in `deploy/README.md`,
then start the backend stack:

```sh
docker compose -f compose.dev.yml up --build
```

Run the Go checks directly with:

```sh
make fmt-check
make vet
make test-unit
```

The optional `misty` developer CLI lives in the separate
`misty-org/misty-cli` repository.

## Agent runtime

The agent runtime is part of this backend repository but remains independently
deployable. From `apps/agent-runtime/`:

```sh
npm ci
npm run typecheck
npm test
npm run build
```
