# Misty Agent Runtime

Durable Misty execution built with AI SDK 7 `WorkflowAgent`. The runtime has no Misty database access. Run activation, context, checkpoints, approval resumption, cancellation, and completion cross the signed Go control-plane API; application tools use Misty's authenticated MCP endpoint.

## Cloud-to-MCP flow

1. Go starts a Vercel Workflow run through the signed lifecycle API.
2. The workflow activates and reads its authoritative context.
3. At run start, Vercel exchanges its signed runtime identity for a five-minute bearer, discovers the run-scoped MCP catalog, and turns connected remote JSON Schemas into model-visible tools.
4. Immediately before each tool call, Vercel exchanges for a fresh bearer and the official TypeScript MCP client calls the Go SDK's stateless `POST /mcp` endpoint over the same HTTPS API base.
5. Go revalidates the active runtime binding, user, Space capabilities, approval state, and device grants on every request, then executes through the canonical Agent Toolbox and audit journal.

Tokens cannot be reused for another Misty run or Vercel Workflow run. During a rolling deployment, the runtime falls back to the signed legacy tool endpoint only when token discovery is unavailable and before any tool execution begins.

## Worlds

- Local development: omit `WORKFLOW_TARGET_WORLD` to use Workflow's local world.
- Self-hosted: set `WORKFLOW_TARGET_WORLD=@workflow/world-postgres` and `WORKFLOW_POSTGRES_URL`; run `npm run world:setup --workspace=@misty/agent-runtime` before the worker starts.
- Vercel: deploy the repository with `npm run build:agent-runtime` as the build command. Workflow selects the managed Vercel world in that environment.

A Vercel Workflow is the durable execution host for Misty's agent loop, not the
browser frontend and not the MCP server. In development, `misty server up` runs
the workflow runtime, its PostgreSQL world, the Go API, and the MCP endpoint in
local containers. In the managed production topology, the Go API and MCP
endpoint stay on the VPS while only this runtime is deployed to Vercel.

## Required environment

- `MISTY_INTERNAL_API_BASE`: HTTPS base for the Go API. A reverse-proxy prefix such as `/api` is preserved when resolving the signed internal routes and `/mcp` (private `http://api:8080` is allowed in Compose).
- `MISTY_AGENT_RUNTIME_CONTROL_SECRET`: base64-encoded secret of at least 32 bytes, shared with Go.
- `MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS`: optional previous secret during rotation.
- `AI_GATEWAY_API_KEY`: AI Gateway credential outside Vercel OIDC environments.

The Go API uses this workflow runtime for every assigned Space task. Configure its endpoint with `MISTY_AGENT_RUNTIME_URL`; there is no legacy/runtime mode switch. `misty server up` starts the local Postgres world, setup job, runtime, and Go API together.

## Vercel CLI deployment

Create one shared secret and keep it out of shell history:

```sh
openssl rand -base64 32
```

From `agent-runtime/`, link the Vercel project, add the production variables,
and deploy. `MISTY_INTERNAL_API_BASE` is the externally reachable Go API base,
including any reverse-proxy prefix such as `/api`.

```sh
vercel link
vercel env add MISTY_INTERNAL_API_BASE production
vercel env add MISTY_AGENT_RUNTIME_CONTROL_SECRET production --sensitive
# Run these two only when rotation is active or AI Gateway requires a key:
vercel env add MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS production --sensitive
vercel env add AI_GATEWAY_API_KEY production --sensitive
vercel deploy --prod
```

The previous secret and AI Gateway key are optional when no rotation is active
or Vercel OIDC supplies the gateway identity. Configure the resulting HTTPS
deployment URL as `MISTY_AGENT_RUNTIME_URL` on Go. Do not assume a hostname such
as `agents.mistysys.com`; use the Vercel URL or a custom domain that you have
actually attached to the runtime project.

The signed Go start request also carries
`MISTY_AGENT_RUNTIME_INTERNAL_API_URL` for each run. The Vercel
`MISTY_INTERNAL_API_BASE` value is retained as a rolling-deployment fallback;
set both to the same reachable API base.
