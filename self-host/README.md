# Misty self-hosted server

This stack runs the browser API, PostgreSQL, Yjs collaboration, and the multi-step Agent runtime. Automation services and app installation services are retired. Existing automation volumes are not deleted by an upgrade; export or archive them before removing volumes manually.

Use immutable release image digests. Configure the API origin and WebSocket reverse proxy, database credentials, authentication signing keys, Library storage, collaboration keys, and your own AI provider credentials. Start with `docker compose --env-file .env -f compose.yml up -d`.

## Instance-wide agent BYOK

The Vercel AI SDK agent runtime supports direct OpenAI, Anthropic, Google, and
OpenAI-compatible providers. Direct mode does not require a Vercel account or
`AI_GATEWAY_API_KEY`. The operator chooses one model for the instance; users see
that model in the agent/companion model selector. Choose a model that supports
tool calling and images for browser and companion tasks.

In your self-host `.env`, replace the placeholders with your provider model ID
and API key:

```dotenv
MISTY_AGENT_MODEL_PROVIDER=openai
MISTY_AGENT_MODEL=openai/YOUR_MODEL_ID
MISTY_AGENT_MODEL_API_KEY=YOUR_PROVIDER_API_KEY
```

| Provider | `MISTY_AGENT_MODEL_PROVIDER` | `MISTY_AGENT_MODEL` format |
| --- | --- | --- |
| OpenAI | `openai` | `openai/YOUR_MODEL_ID` |
| Anthropic | `anthropic` | `anthropic/YOUR_MODEL_ID` |
| Google Gemini | `google` | `google/YOUR_MODEL_ID` |
| OpenAI-compatible endpoint | `openai-compatible` | `openai-compatible/YOUR_MODEL_ID` |

For OpenAI-compatible servers, also set the API base URL (including `/v1` when
required by that server):

```dotenv
MISTY_AGENT_MODEL_PROVIDER=openai-compatible
MISTY_AGENT_MODEL=openai-compatible/YOUR_MODEL_ID
MISTY_AGENT_MODEL_BASE_URL=http://host.docker.internal:11434/v1
# MISTY_AGENT_MODEL_API_KEY=YOUR_PROVIDER_API_KEY
```

An API key is optional only for OpenAI-compatible endpoints that accept unauthenticated
requests. `localhost` inside a container means that container; use a reachable
hostname or Compose service name. Docker Desktop provides `host.docker.internal`;
Linux deployments need an appropriate host mapping or network address.

Compose passes the same configuration to the API and agent runtime. For separately
deployed services, configure all four variables consistently on both. Restart both
services after changing routing. Active runs pinned to a different model will fail
explicitly rather than silently changing provider. Credentials are resolved in model
execution steps and are not serialized into workflow history. Direct mode never
falls back to Gateway or a different provider. OpenAI-compatible endpoints receive
no managed reasoning-effort setting, for compatibility with local models.

For the managed development layout, these settings belong in
`server/.env/dev/integrations/ai.env`; run `misty server up` after editing it.

Existing Gateway deployments keep working with `AI_GATEWAY_API_KEY` (or Vercel
OIDC) and the default `MISTY_AGENT_MODEL_PROVIDER=gateway`. Self-hosted Gateway
operators may also set `MISTY_AGENT_MODEL` to a Gateway model ID. Hosted deployments
retain the release-managed model policy and reject these overrides.

This BYOK configuration covers the Vercel AI SDK **agent language-model path**.
Library analysis/embeddings and companion transcription/speech still use their
existing backend configuration; setting an agent key alone does not enable those
services. In particular, the current speech generation path still requires AI
Gateway. BYOK does not resolve the separate self-hosting migration limitations below.

Create the first administrator bootstrap token with:

```sh
docker compose --env-file .env -f compose.yml run --rm --entrypoint misty-admin api bootstrap-token
```

Enrollment invitations and Space membership remain separate. Keep account authentication and membership checks enabled.

## Billing migration status

Independent servers without a billing adapter have a finite 2 GB storage ceiling
per account and per Space. Hosted deployments must configure the HTTP billing
adapter and use its subscription entitlements. Billing lookup failures return an
unavailable error rather than granting storage. AI metering is account-wide and
separate from Space storage; `/billing/ai-usage` does not read storage counters.

The optional billing adapter contract is documented in the [wiki](https://github.com/misty-org/misty/wiki/Server-billing-adapter). The current Go billing paths and self-host enrollment entitlement checks have not yet completed their cutover. Do not treat this intermediate checkout as an independently self-hostable release until the migration acceptance checklist is complete.

## Recovery

Reset a password through `misty-admin reset-password --email person@example.com`, supplying the new password on standard input. Disable an account and revoke its sessions through `misty-admin disable-account --email person@example.com`.

Back up PostgreSQL and Library blobs together before updates. Keep all signing/encryption keys stable across restarts. Do not remove archived application data merely because its old routes have been retired.

The desktop does not elect or start this server. LAN DNS, a VPN, a tunnel, or a reverse proxy may expose it, provided HTTPS is trusted and WebSocket upgrades are forwarded.
