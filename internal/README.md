# Server ownership

The public server supports Accounts, Spaces, Agents and browser/device Sync.
Commercial implementation lives in the separate private Go billing repository.

| Location | Responsibility |
| --- | --- |
| `accounts` | Registration, profile/settings/avatar persistence, sessions and account RLS scopes. |
| `spaces` | Membership permissions, feature access and upload policy. |
| `agents` | Model and tool execution, companion voice, Library analyzers and provider calls. |
| `sync` | Browser/device HTTP and WebSocket transport, session storage and synchronization. |
| `library` | Per-attempt admission and raw-usage completion for Library providers. |
| `billingadapter` | Optional authenticated protocol, reservations, durable outbox and recovery; no charging policy. |
| `workflows` | Shared tool-execution types and errors for durable Agent runs. The Studio workflow engine was removed. |
| `integrations` | Composio connected-app sessions. |
| `app` | Composition, route mounting and retained background workers. |
| `platform` | Shared configuration/auth/transport/database infrastructure plus existing product HTTP/SQL adapters. |

`platform/httpapi` and `platform/postgres` still contain current first-party
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
