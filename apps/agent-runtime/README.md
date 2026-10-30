# Misty Agent Runtime

The durable agent loop for Misty, built on the AI SDK's `WorkflowAgent` and the
Workflow engine. It has no database access of its own: run activation, context,
checkpoints, steering, time budgets and completion go through the Go API's signed
internal routes (`/internal/agent-runtime/runs/{runID}/…`), and every tool call
goes to Misty's authenticated MCP endpoint (`/mcp`).

## How a run works

1. Go starts a run with a signed `POST /v1/runs`.
2. The workflow (`workflows/space-task-agent.ts`) activates, reads its context,
   and exchanges its runtime identity for a short-lived MCP bearer
   (`mcp-token`). It lists the run's tools from `/mcp` and shows them to the
   model under their Misty names, with dots as underscores (`note.search` is
   `note_search`).
3. Each model turn is counted by Go first (the run's turn limit). Tool calls run through
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
Go picks the model and sends its route with the call. Provider errors return a
status, never the provider's body. Live transcription is a WebSocket at
`/v1/live/transcribe` (`src/live/`).

## Models

People bring their own model keys (`src/model-provider.ts`). Model IDs are
`<provider>/<model as that provider names it>`, for example
`anthropic/claude-sonnet-5-5`, `openrouter/anthropic/claude-sonnet-5.5` or
`ollama/qwen3:14b`. Every call carries a route from Go:

```json
{ "provider": "account", "reasoning": "low",
  "credential": { "provider": "anthropic", "apiKey": "…", "baseURL": "…" } }
```

- `account` runs on exactly that credential, which must be for the model's
  provider. It never falls back to the runtime's own keys. An account
  `baseURL` must be a public http(s) address (`host.docker.internal` is
  allowed for a local Ollama).
- `instance` (`{ "provider": "instance" }`) runs on the runtime's environment
  keys below, for single-user self-hosting and evals. An `openai/`,
  `anthropic/` or `google/` model with no key of its own goes through the AI
  Gateway when `AI_GATEWAY_API_KEY` is set.

With no usable key a call fails with `model_key_needed` (HTTP 409 for one-shot
calls; an agent run fails at once rather than retrying).

| Provider | Language | Embedding | Speech | Transcription | Instance env |
| --- | --- | --- | --- | --- | --- |
| `openai` | Responses | yes | yes | yes | `OPENAI_API_KEY`, `OPENAI_BASE_URL` |
| `anthropic` | Messages | | | | `ANTHROPIC_API_KEY` (or `CLAUDE_API_KEY`), `ANTHROPIC_BASE_URL` |
| `google` | Gemini API | yes | | | `GEMINI_API_KEY` (or `GOOGLE_GENERATIVE_AI_API_KEY`) |
| `openrouter` | chat completions | yes | | | `OPENROUTER_API_KEY`, `OPENROUTER_BASE_URL` |
| `gateway` | Vercel AI Gateway | yes | yes | yes | `AI_GATEWAY_API_KEY` (or `VERCEL_OIDC_TOKEN`), `AI_GATEWAY_BASE_URL` |
| `nvidia` | chat completions | yes | | | `NVIDIA_API_KEY`, `NVIDIA_BASE_URL` |
| `ollama` | chat completions | yes | | | `OLLAMA_BASE_URL` (default `http://host.docker.internal:11434/v1`), `OLLAMA_API_KEY` |
| `openai-compatible` | chat completions | yes | | | `OPENAI_COMPATIBLE_BASE_URL` (required), `OPENAI_COMPATIBLE_API_KEY` |
| `elevenlabs` | | | yes | | `ELEVENLABS_API_KEY` |
| `soniox` | | | | yes | `SONIOX_API_KEY` |

`nim/` is kept as an alias of `nvidia/` and `gemini/` of `google/`.
Chat-completions providers get screenshots moved out of tool results into a
user message; NVIDIA, Ollama and OpenAI-compatible servers get no
`reasoning_effort`.

Keys never reach workflow history, logs, errors or responses. `InstanceModel`
keeps only the public model ID and run identity in workflow history; each model
step reads the run's route from Go (`model-provider`), uses the key for that
call and drops it. Errors are scrubbed of the key before they are thrown or
logged.

## Environment

- `MISTY_INTERNAL_API_BASE`: the Go API base (a proxy prefix such as `/api` is kept).
- `MISTY_AGENT_RUNTIME_CONTROL_SECRET`: base64 secret of at least 32 bytes, shared
  with Go; `MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS` during rotation.
- `WORKFLOW_TARGET_WORLD=@workflow/world-postgres` and `WORKFLOW_POSTGRES_URL`;
  without them Workflow uses its local world.
- `MISTY_AGENT_MODEL`: the instance's default agent model (`provider/model`).
- The instance model keys in the table above, all optional.
- `MISTY_RUNTIME_LIVE_TRANSCRIBE=1` turns on live transcription. Its signed
  ticket carries no key, so the relay asks Go for the session's route
  (`POST /internal/voice/live/credential`, `{session_id, user}`) and uses the
  account's Soniox key, or `SONIOX_API_KEY` for an `instance` route.

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
