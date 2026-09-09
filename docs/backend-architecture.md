# Backend architecture

The Go-to-Hono replacement was cancelled on 2026-09-06. Go remains the main API
and background-job runtime. Do not resume the former migration goal or rebuild
the removed main API replacement. The separate billing service is retained.

- Go owns authentication, authorization, product data, quotas and job admission.
- `apps/payments` is the separate billing service. Go still handles live billing
  until the integration and ownership handover are completed.
- `apps/agent-runtime` keeps Vercel AI SDK and durable workflows in TypeScript,
  using the existing signed interface to the Go API.
- `apps/journal-collab` and `apps/self-host-collab` keep Yjs document synchronization
  and collaboration in TypeScript. Go supplies authorization tickets and domain data.
- Root Node tooling builds/tests billing and maintains the public SDK contract
  snapshot and Go dispatch table. Hono is used inside billing only.

The rollback preserves the existing Go onboarding, official-app, SDK integration,
and other product changes. PostgreSQL migration history and compatibility fields
remain because earlier revisions may already be applied. No migration, database
rollback, native-job adoption or deployment was performed by this rollback.

The removed main API implementation and its validation notes are preserved in verified
local recovery archives outside the repository. They are historical material,
not active implementation instructions. Existing AI and collaboration services
were preserved byte-for-byte.
