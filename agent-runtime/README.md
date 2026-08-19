# Misty Agent Runtime

Durable assigned-Task execution for Misty, built with AI SDK 7 `WorkflowAgent`. The runtime has no Misty database access; every context read, tool call, checkpoint, and completion crosses the signed Go control-plane API.

## Worlds

- Local development: omit `WORKFLOW_TARGET_WORLD` to use Workflow's local world.
- Self-hosted: set `WORKFLOW_TARGET_WORLD=@workflow/world-postgres` and `WORKFLOW_POSTGRES_URL`; run `npm run world:setup --workspace=@misty/agent-runtime` before the worker starts.
- Vercel: deploy the repository with `npm run build:agent-runtime` as the build command. Workflow selects the managed Vercel world in that environment.

## Required environment

- `MISTY_INTERNAL_API_BASE`: HTTPS URL for the Go API (private `http://api:8080` is allowed in Compose).
- `MISTY_AGENT_RUNTIME_CONTROL_SECRET`: base64-encoded secret of at least 32 bytes, shared with Go.
- `MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS`: optional previous secret during rotation.
- `AI_GATEWAY_API_KEY`: AI Gateway credential outside Vercel OIDC environments.

The Go API uses this workflow runtime for every assigned Space task. Configure its endpoint with `MISTY_AGENT_RUNTIME_URL`; there is no legacy/runtime mode switch. `misty server up` starts the local Postgres world, setup job, runtime, and Go API together.
