# Misty Server

Misty's backend uses Go for the main API and background jobs, with separate
TypeScript services for AI SDK workflows, Yjs collaboration, and billing. Each service
keeps its own deployment and dependency boundary. See [backend architecture](docs/backend-architecture.md).

## Repository layout

- `cmd/` and `internal/` — Go API entrypoints and application packages.
- `internal/platform/postgres/migrations/` — existing PostgreSQL schema migrations.
- `apps/payments/` — isolated billing service; Go integration and ownership handover remain pending.
- `apps/agent-runtime/` — durable AI workflow execution runtime.
- `apps/journal-collab/` — collaborative journal and drawing worker.
- `apps/self-host-collab/` — independently packaged self-hosted Yjs service.
- `deploy/` and `self-host/` — managed and self-hosted deployment assets.

## Local development

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

## Public SDK contracts

The root Node package builds the separate billing service and maintains the
reviewed public SDK snapshot; it does not run the Go API. Use `npm ci && npm run contracts:check` to
verify the Go dispatch routes. `npm run contracts:sync` updates them from the
reviewed sibling SDK package or an explicit package archive.

## Billing service

Billing stays independently packaged in `apps/payments`. Run `npm ci`,
`npm run typecheck:payments`, `npm run test:payments`, and
`npm run build:payments` to verify it. Build its image with
`docker build -f apps/payments/Dockerfile .`.

The service is not cut over: Go still owns live billing. See
[billing status](docs/billing-service.md) for the remaining Go integration.

## Agent runtime

The agent runtime is part of this backend repository but remains independently
deployable. From `apps/agent-runtime/`:

```sh
npm ci
npm run typecheck
npm test
npm run build
```
