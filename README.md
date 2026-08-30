# Misty Server

Misty's backend repository contains the Go API and its supporting backend
runtimes. Keeping them together makes the deployment contract, local Compose
stack, and end-to-end tests change as one unit.

## Repository layout

- `cmd/` and `internal/` — Go API entrypoints and application packages.
- `migrations/` — PostgreSQL schema migrations.
- `agent-runtime/` — durable AI workflow execution runtime.
- `cloudflare/journal-collab/` — collaborative journal and drawing worker.
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

## Agent runtime

The agent runtime is part of this backend repository but remains independently
deployable. From `agent-runtime/`:

```sh
npm ci
npm run typecheck
npm test
npm run build
```
