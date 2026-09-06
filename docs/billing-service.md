# Separate billing service

The intended backend has a Go API and independent services for AI SDK workflows,
Yjs collaboration, and billing. Reverting the main API migration does not cancel
the billing split. `apps/payments` retains its TypeScript/Hono implementation;
its build and container no longer require `apps/api`.

## Implemented

The service includes Stripe webhook ingestion, leased background workers,
checkout and portal commands, subscription reconciliation, signed entitlement
outbox delivery, billing summaries, account closure, and explicit legacy-data
import commands. Its billing schema and role checks remain isolated from the
application database. Shared runtime/database/message packages support this
service; they do not replace the Go API.

## Remaining before live use

Go still owns live billing. The service's readiness gate remains closed. The
Go API needs compatible signed checkout/portal/summary/closure clients and an
entitlement receiver, with the existing quota and lifetime-grant accounting
preserved. Legacy import, reconciliation, and ownership handover need a bounded
rehearsal before any deployment change. No live billing ownership, Stripe
configuration, database contents, or running services were changed here.

The prior delivery and initialization integration tests used the removed Hono
API as their receiver. Those tests remain available in commit `173fc66` under
`apps/payments/src/modules/entitlements`; they do not establish compatibility
with Go. The standalone billing tests are retained. A Go-to-billing integration
check is still required before declaring the service ready.

## Local commands

- `npm run typecheck:payments`
- `npm run test:payments`
- `npm run build:payments`
- `npm run test:payments:integration` with an isolated `MISTY_TEST_DATABASE_URL`
- `npm run dev:payments` with explicit billing database, Stripe test-mode, and signing configuration

Use the existing import commands only after reviewing their dry-run output.
Do not infer authorization to migrate or alter live billing from this document.
