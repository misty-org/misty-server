# Misty Server — Codebase Guide

Go HTTP API (chi + Postgres) backing the Misty desktop app in the sibling repo
`misty`.

## Where things go

```
cmd/
  misty-server/          Main binary.
  <tool>/                One-off tools.

internal/
  <domain>/              Domain logic and types.        package <domain>
    http/                chi handlers for the domain.   package http
    store/               Postgres persistence.          package store

  config/                Environment parsing. THE ONLY place os.Getenv is legal.
  email/ metrics/ security/ telemetry/
  httpx/                 Shared HTTP middleware: rate limiting, abuse guard,
                         egress budget, client IP, realtime transport.
  pg/                    Connection pool, tx helpers, migrations, RLS.
  app/                   Composition root: wiring, route table, lifecycle.

test/
  integration/ contract/ testkit/    Cross-cutting tests only.
```

Unit tests are colocated as `_test.go` beside the code they test, per Go
convention. `test/` holds only tests that span packages.

## Domains

`accounts` `billing` `spaces` `tasks` `roadmaps` `library` `discovery`
`notes` `drawings` `agents` `workflows` `integrations` `journal`

A domain is a thing the product has, not a technical layer. If a package exceeds
~30 files, it is probably two domains.

## Package naming

**A package's name always equals its directory name.** Short, lowercase, one
word, no underscores, no mixedCaps.

This is enforced by `TestPackageNameMatchesDirectory` in
`test/contract/architecture/architecture_test.go`.

Because domains share the generic names `http` and `store`, the composition root
aliases them at the import site — and it is the only place that needs to:

```go
// internal/app/run.go
import (
    spaceshttp  "github.com/kannachi323/misty/server/internal/spaces/http"
    spacesstore "github.com/kannachi323/misty/server/internal/spaces/store"
    taskshttp   "github.com/kannachi323/misty/server/internal/tasks/http"
)
```

Everywhere else, no alias and no stutter:

```go
svc := spaces.NewService(store)
```

Do not name a package after its parent (`spaceshttp` in a directory called
`http`) and do not give a directory a name its package contradicts. That
mismatch is what produced the old `httpapi`/`package api` confusion.

## Import direction

```
app  →  <domain>/http  →  <domain>  →  <domain>/store
                              ↓
                    config, pg, httpx, telemetry, …
```

- No domain imports `internal/app`.
- A domain's `http/` may import its own `store/`.
- **No domain imports another domain's `store/`.** Cross-domain access goes
  through the other domain's root package.
- Production code never imports `test/`.

Enforced by `TestDomainImportDirection`.

## Interfaces

Go convention: **accept interfaces, return structs.** Declare an interface where
it is *consumed*, sized to what the consumer actually needs.

Do not create standalone `ports.go` files enumerating every method a domain might
need. (The repo had seven of these; none were implemented, and they described an
architecture the code did not follow.)

## File size and naming

Files are `snake_case.go`, named for the **concept** they contain.

Hard cap **500 lines** (`TestHandwrittenGoFilesRespectHardMaximum`).

When a file exceeds the cap, split it into another concept — or recognise that
the package wants splitting into two domains. Never split mechanically by
function name. Files like `media_search_min_int64.go` (20 lines, one helper) and
`library_organization_create_library_album_folder.go` are what that mistake
looks like; the cap is there to prompt design, not text surgery.

## Configuration

`os.Getenv` and `os.LookupEnv` are legal **only** in `internal/config/` and
`cmd/`. Everything else takes config as a dependency.

Every environment variable must appear in
`test/contract/architecture/fixtures/environment-contract.env`, or
`TestEnvironmentContractIsExplicit` fails.

## Commands

```bash
./test.sh              # bootstraps Docker Postgres on :5435, runs go test ./...
go test ./...          # works once test.sh has brought Postgres up
make lint              # golangci-lint
go build ./...
```

## Adding an endpoint — checklist

1. Handler in `internal/<domain>/http/`.
2. Queries in `internal/<domain>/store/`.
3. Business rules in `internal/<domain>/` — not in the handler.
4. Route registered in `internal/app/`.
5. New env vars added to `config/` and to the environment-contract fixture.
6. `./test.sh` green.
