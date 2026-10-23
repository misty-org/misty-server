# Misty Agent Runtime

The durable agent loop for Misty, built on the AI SDK's `WorkflowAgent` and the
Workflow engine. It has no database access of its own: run activation, context,
checkpoints, steering, budgets and completion go through the Go API's signed
internal routes (`/internal/agent-runtime/runs/{runID}/…`), and every tool call
goes to Misty's authenticated MCP endpoint (`/mcp`).

## How a run works

1. Go starts a run with a signed `POST /v1/runs`.
2. The workflow (`workflows/space-task-agent.ts`) activates, reads its context,
   and exchanges its runtime identity for a short-lived MCP bearer
   (`mcp-token`). It lists the run's tools from `/mcp` and shows them to the
   model under their Misty names, with dots as underscores (`note.search` is
   `note_search`).
3. Each model turn is admitted and metered by Go first. Tool calls run through
   the MCP client; device work and user actions wait on durable hooks
   (`/v1/devices/:hookToken`).
4. The model reports the outcome with `agent_finish`. The first finish with
   turns left is checked once (`workflows/finish-review.ts`).

Where things live:

- `workflows/`: the run loop, its tools and agent (`run-tools.ts`), per-turn
  pacing (`turn-pacing.ts`) and how a run ends (`run-ending.ts`).
- `src/context/`: keeping the transcript inside the model's window (stale page
  snapshots, the working set, cleared tool results, summaries of older steps)
  and prompt caching.
- `src/tool-calls.ts`, `src/mcp-runtime.ts`: tool transport;
  `src/tool-monitor.ts`, `src/tool-outcomes.ts`: what a call's result means for
  the run.
- `src/control-plane*.ts`: the signed calls to Go.

## One-shot model calls

Go sends model calls outside agent runs here, signed like the run routes:
`POST /v1/models/text`, `embed`, `transcribe`, `transcribe-diarized`,
`voice-notes`, `day-summary` and `speech` (`src/model-calls.ts` and siblings).
Go picks the model and meters the call; the runtime calls it on Misty's keys
and reports usage. Provider errors return a status, never the provider's body.
Live transcription is a WebSocket at `/v1/live/transcribe` (`src/live/`).

## Models

Every model runs on Misty's own keys (`src/model-provider.ts`). `openai/…` and
`anthropic/…` models call their maker directly when `OPENAI_API_KEY` or
`ANTHROPIC_API_KEY` is set; everything else goes through the AI Gateway.
`nim/…` and `gemini/…` models are for development evals only.
`InstanceModel` keeps only public model and run identity in workflow history and
resolves credentials when the model runs.

## Environment

- `MISTY_INTERNAL_API_BASE`: the Go API base (a proxy prefix such as `/api` is kept).
- `MISTY_AGENT_RUNTIME_CONTROL_SECRET`: base64 secret of at least 32 bytes, shared
  with Go; `MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS` during rotation.
- `WORKFLOW_TARGET_WORLD=@workflow/world-postgres` and `WORKFLOW_POSTGRES_URL`;
  without them Workflow uses its local world.
- `AI_GATEWAY_API_KEY` (or `VERCEL_OIDC_TOKEN`), optionally `OPENAI_API_KEY`,
  `ANTHROPIC_API_KEY` and `MISTY_AGENT_MODEL`.
- `ELEVENLABS_API_KEY` for speech; `SONIOX_API_KEY` for transcription, and
  `MISTY_RUNTIME_LIVE_TRANSCRIBE=1` to turn on live transcription.

`misty server up` runs this runtime, its Postgres world and the Go API in local
containers (`server/compose.dev.yml`). Production runs the same image on the
VPS (`server/compose.prod.yml`), whose `agent-runtime-setup` job migrates the
world first.

## Development

```sh
npm install
npm run typecheck
npm test
```

Evals live in `evals/` and run against a local stack: `eval:odysseys` (the
Odysseys benchmark through the product path, see `evals/odysseys/README.md`),
`eval:pointing`, `eval:voice` and `eval:free-model`.
