# Server ownership

The server supports Accounts, Spaces, Agents and browser/device Sync. It has
no billing: every feature is open to every account, and models run on keys
the person brings from their device (`byok`) or on the operator's own keys.

| Location | Responsibility |
| --- | --- |
| `accounts` | Registration, profile/settings/avatar persistence, sessions and account RLS scopes. |
| `spaces` | Membership permissions, feature access and upload policy. |
| `agent` | Model and tool execution, companion voice, embeddings, transcription and provider calls. |
| `agenttools` | The Agent Toolbox registry: tool descriptors, schemas, aliases and execution checks. |
| `aimodels` | Internal model roles and how an account's choices resolve. |
| `modelruntime` | The client for the agent runtime, which makes every model call. |
| `console` | The localhost operator console (templ + htmx). |
| `browsersync` | Browser/device HTTP and WebSocket transport, session storage and synchronization. |
| `byok` | The `X-Misty-Models` header: decoding, the in-memory per-account store and role lookup. |
| `workflow` | Shared tool-execution types and errors for durable Agent runs. The Studio workflow engine was removed. |
| `integrations` | Composio connected-app sessions. |
| `app` | Composition, route mounting and retained background workers. |
| `platform` | Shared configuration/auth/transport/database infrastructure plus existing product HTTP/SQL adapters. |

`platform/httpapi` and `platform/db` still contain current first-party
feature adapters. Their filenames identify their feature; Accounts and Sync
entry points delegate to their domain implementations. Retaining these adapters
preserves current callers without duplicating behavior. Journal and discovery
adapters live under `platform`.

Retired app installation/session/RPC, Activepieces, user routines, waitlist,
the SDK provider system, MCP connectors, Studio workflows, tool-approval waits,
native GitHub/Figma/Slack/Discord/Instagram integrations and social messaging
are absent from runtime composition. Connected tools, Space collaboration,
Library and Agent multi-step execution remain supported. Account isolation and
Space membership checks remain mandatory. Database archives preserve retired
user records without exposing them to the runtime role.
