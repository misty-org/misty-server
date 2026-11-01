# Misty Agent Runtime

The durable agent loop for Misty, built on the AI SDK's `WorkflowAgent` and the
Workflow engine. It runs only on the person's computer: the desktop app starts it
as a helper process (`src-tauri/src/infra/agent_runtime/`, see
docs/agents/runtime.md), and every model call happens here on that device's own
keys. It has no database access of its own: run activation, context, checkpoints,
steering, time budgets and completion go through the Go API's internal routes
(`/internal/agent-runtime/runs/{runID}/…`), signed with the run's own key, and
every tool call goes to Misty's authenticated MCP endpoint (`/mcp`).

## How a run works

1. The app passes a delivery from the server to `POST /v1/runs`, signed with this
   launch's secret. A start carries the run's key and its start receipt.
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

## Model calls outside runs

The app's own model calls go to `POST /v1/device/*` (`src/device-calls.ts`):
dictation cleanup, voice transcription, the screen planner, companion pointing,
extraction, embeddings, day summaries, voice notes, diarized transcription and
the model test. Provider errors return a status, never the provider's body. Live
transcription is a WebSocket at `/v1/live/transcribe` (`src/live/`) and the
companion's spoken conversation one at `/v1/device/voice` (`src/voice/`).

## Models

The app sends this device's model keys and choices to `POST /v1/local/models`
(`src/device-models.ts`) whenever they change; they live only in this process's
memory. Model IDs are `<provider>/<model as that provider names it>`, for example
`anthropic/claude-sonnet-5-5`, `openrouter/anthropic/claude-sonnet-5.5` or
`ollama/qwen3:14b`. Each call runs on the device's key for the model's provider
and never on any other (`src/model-provider.ts`). A base URL may name a server on
the person's own computer or network, such as a local Ollama.

With no usable key a call fails with `model_key_needed` (HTTP 409 for a device
call; an agent run fails at once rather than retrying).

## Web search

`web.search` runs here, not on the server (`src/web-search/`): the model document's
`search` carries the device's search service (`decodo` or `serper`) and key. A run
with one adds the tool to its catalog and tells the server in its `context` request
(`device_tools`), so the prompt describes it; a run without one doesn't offer it.
Each provider is a small adapter behind one interface, answers are cached in memory
for 6 hours, and results stay marked untrusted.

| Provider | Language | Embedding | Speech | Transcription |
| --- | --- | --- | --- | --- |
| `openai` | Responses | yes | yes | yes |
| `anthropic` | Messages | | | |
| `google` | Gemini API | yes | | |
| `openrouter` | chat completions | yes | | |
| `gateway` | Vercel AI Gateway | yes | yes | yes |
| `nvidia` | chat completions | yes | | |
| `ollama` | chat completions (default `http://127.0.0.1:11434/v1`) | yes | | |
| `ollama-cloud` | chat completions | yes | | |
| `openai-compatible` | chat completions (needs a base URL) | yes | | |
| `chatgpt` | ChatGPT sign-in (docs/byok.md) | | | |
| `elevenlabs` | | | yes | |
| `soniox` | | | | yes |

`nim/` is kept as an alias of `nvidia/` and `gemini/` of `google/`.
Chat-completions providers get screenshots moved out of tool results into a
user message; NVIDIA, Ollama and OpenAI-compatible servers get no
`reasoning_effort`.

Keys never reach workflow history, logs, errors or responses. `InstanceModel`
keeps only the public model ID in workflow history; each model step reads the
device's key, uses it for that call and drops it. Errors are scrubbed of the key
before they are thrown or logged.

## Environment

The app sets all of these when it starts the runtime:

- `MISTY_RUNTIME_DESKTOP=1`: the runtime takes the device's models and serves
  `/v1/local/models`, `/v1/device/*` and the voice socket.
- `MISTY_AGENT_RUNTIME_CONTROL_SECRET`: a fresh base64 secret for each launch,
  which signs every `/v1` call and live and voice tickets.
- `WORKFLOW_LOCAL_DATA_DIR` and `WORKFLOW_LOCAL_BASE_URL`: Workflow's local
  world, so a run continues after Misty quits and opens again.
- `MISTY_RUNTIME_LIVE_TRANSCRIBE=1` turns on live transcription.

## Development

```sh
npm install
npm run typecheck
npm test
npm run build
```

The app's development build runs this checkout's `.output`, so build it first.
