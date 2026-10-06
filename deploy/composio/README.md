# Connected apps (Composio Cloud)

Misty's agents reach Gmail, Google Drive, Google Calendar, Slack, Notion, GitHub
and every other Composio toolkit through one Composio Cloud session per Misty
account. Composio stores and refreshes provider credentials and runs the tools;
Misty owns connection prompts, approvals, the effect journal and billing.

Status (October 4, 2026): the session gateway replaced the calendar-only
adapter. The API, desktop and agent runtime compile and start locally; runtime
tests, gateway tests and the disposable-Postgres approval contract pass. The
full frontend and server suites still have failures, and the live provider
workflow is waiting for Misty sign-in. Phases 1 and 2 are not yet accepted;
see **Verify** below.

## Configure

1. Keep the dashboard project key as `COMPOSIO_API_KEY` in the ignored
   `server/.env/dev/composio.env` (prod: `server/.env/prod/composio.env`).
   Docker Compose loads it for the API only. Do not run project provisioning,
   rotate the key or put it in `VITE_*` variables.
2. Copy `runtime.env.example` to `server/.env/dev/integrations/composio.env`.
   `MISTY_COMPOSIO_DEPLOYMENT=cloud` is the explicit opt-in; a key alone does
   not enable connected apps.
3. Recreate the API. Composio-managed auth covers most toolkits without any
   auth config; a toolkit without a managed scheme needs an auth config in the
   Composio dashboard before users can connect it. Custom OAuth apps (for
   Misty's own branding on Google's consent screen) are a launch-checklist
   item, not a prerequisite.

The CLI ignores the retired `MISTY_COMPOSIO_URL` and
`MISTY_COMPOSIO_CALENDAR_*` settings in older files.

## How it works

- **Identity.** Each Misty account maps to a pseudonymous Composio user
  (`misty_` + SHA-256 of the account ID). Composio never receives Misty user IDs
  or email addresses.
- **Session.** Created on first use with every toolkit enabled, the remote
  workbench and bash tools disabled, and Composio's in-chat connection prompts
  disabled. The session ID is stored in `composio_sessions` and recreated if
  Composio no longer has it.
- **Agent tools.** Every chat, agent and task run gets `apps.search`,
  `apps.schemas`, `apps.connected`, `apps.connect` and `apps.execute`
  (the model sees `apps_search` and so on). Agents inherit the account's
  connected apps; there are no per-agent bindings.
- **Connect card.** `apps.connect` shows a **Connect** card in the chat and
  waits up to 40 seconds while the user signs in; the model calls it again to
  keep waiting. Sign-in links must be HTTPS links on `composio.dev`.
- **Approvals.** `apps.execute` classifies each tool with Composio's behavior
  tags and the action it names. Sends, posts, shares, invites, payments,
  access changes and deletes wait for an **Approve** card while the account
  setting *Ask before acting for you* (Settings → Agents → Connections) is on,
  which is the default. Approvals are bound to the exact tool, arguments and
  account, expire after 15 minutes and are used once.
- **Effects.** Reads run directly and may be retried. Writes are journaled
  under the run and call ID with encrypted results. A request Composio rejects,
  or an app error, changed nothing and returns to the model; a request without a
  response is an unknown outcome and stops the run for review.
- **Management.** Agents → Integrations → Apps lists connected apps, connects
  new ones from the full catalog and disconnects them. Disconnecting deletes the
  connected account in Composio; the user can also revoke access at the
  provider.

## Verify

A key, a session, a search or a Connect Link alone does not prove the
integration. In a chat:

1. Ask "list my five latest emails". Expect an `apps_search`, a Connect card for
   Gmail, sign-in, then a result from a read tool with a Composio `log_id`.
2. Ask to create a Google Drive folder. Expect no approval card (a create).
3. Ask to send a test email to yourself. Expect an Approve card that shows the
   exact recipient and subject; approve it and check the sent mail.
4. Disconnect Gmail on the Apps page and confirm the next Gmail request shows a
   Connect card again.

## References

- [Configuring sessions](https://docs.composio.dev/docs/configuring-sessions)
- [Tool Router API (v3.1)](https://docs.composio.dev/reference/api-reference/tool-router)
- [Connected accounts API](https://docs.composio.dev/reference/api-reference/connected-accounts)
- [Meta tools](https://docs.composio.dev/toolkits/meta-tools)
- [White-label authentication](https://docs.composio.dev/docs/auth-configuration/white-labeling)
