# Misty Self-hosted

This bundle runs one Misty API leader, PostgreSQL, the Yjs collaboration service, the assigned-task workflow runtime, and Activepieces Community Edition as Misty's headless automation engine. Desktop clients connect through one Misty HTTPS origin and use Misty's automation editor; people do not open or manage the Activepieces interface. A second Activepieces HTTPS origin remains available only for server-to-server MCP traffic, automation callbacks, and webhooks.

## Install

1. Create the real ignored `.env`, use the immutable image digests from a Misty release, and configure its private values. `AI_GATEWAY_API_KEY` is required for assigned Space agents.
2. Set `MISTY_PUBLIC_API_URL` to the externally reachable HTTPS URL ending in `/api`, `MISTY_COLLAB_PUBLIC_URL` to the same public origin, and `PARTYKIT_HOST` to its hostname. Set `ACTIVEPIECES_PUBLIC_URL` to a second trusted HTTPS origin such as `https://automations.example.com` with no trailing slash.
3. Choose `MISTY_LIBRARY_BACKEND=filesystem` for the bundled `library_data` volume, or `s3` and fill the generic S3 settings. The older `R2_*` names remain supported by the API.
4. Add the Activepieces secrets below, then start the stack with `docker compose --env-file .env -f compose.yml up -d`.
5. Create the one-time administrator token:

   ```sh
   docker compose --env-file .env -f compose.yml run --rm --entrypoint misty-admin api bootstrap-token
   ```

6. In Misty Desktop, sign in to Misty Hosted with an eligible subscription or trial, open Settings → Advanced → Connection, select Self-hosted, and enter the complete server URL. After restart, create the first account with the bootstrap token.
7. Open Agents → Automations in Misty. The API initializes Activepieces, creates the user's isolated automation project, and issues short-lived project credentials automatically. No separate Activepieces account setup or **Connect** step is required.

Generate private Activepieces values once and keep them stable across restarts and upgrades:

```sh
openssl rand -hex 16 # ACTIVEPIECES_ENCRYPTION_KEY
openssl rand -hex 32 # ACTIVEPIECES_JWT_SECRET
openssl rand -hex 32 # ACTIVEPIECES_POSTGRES_PASSWORD
openssl rand -hex 32 # ACTIVEPIECES_REDIS_PASSWORD
```

The API uses `ACTIVEPIECES_JWT_SECRET` as the bootstrap secret for its internal Activepieces service identities, so keep it private and stable. Set `ACTIVEPIECES_POSTGRES_DATABASE=activepieces`,
`ACTIVEPIECES_POSTGRES_USERNAME=activepieces`, and optionally
`ACTIVEPIECES_HOST_PORT=8090`. Route the Activepieces HTTPS origin to
`http://127.0.0.1:8090` from a host reverse proxy, or to `http://activepieces-app:80` from a named
tunnel on the Compose `edge` network. Do not expose its Postgres or Redis services.

Administrators create seven-day enrollment invitations with `POST /api/self-host/invitations`; the response includes the single-use token and an invitation ID. Revoke an unused invitation with `DELETE /api/self-host/invitations/{invitationID}`. Space invitations remain separate and are used only after someone has enrolled an account.

## Recovery

Reset a password by piping the replacement into the admin command:

```sh
printf '%s\n' 'replacement-password' | docker compose --env-file .env -f compose.yml run --rm --entrypoint misty-admin api reset-password --email person@example.com
```

Disable an account and revoke all of its sessions:

```sh
docker compose --env-file .env -f compose.yml run --rm --entrypoint misty-admin api disable-account --email person@example.com
```

## Updates and backups

Back up Misty PostgreSQL, the Library backend, `activepieces_postgres_data`,
`activepieces_redis_data`, and `activepieces_cache` before every update. Stop writes, take the
database and blob backups together, replace image references with tested release digests, run
`docker compose pull`, then `docker compose up -d`.

Activepieces Community Edition has no license fee, but the extra app, worker, Postgres, and Redis
containers consume the existing server's resources. Confirm the host has spare capacity; current
Activepieces guidance sizes each worker at 0.5 vCPU / 1 GB and its recommended Postgres baseline at
2 vCPU / 4 GB.

Misty Desktop never installs, elects, starts, or fails over this leader. LAN DNS, a VPN, Cloudflare Tunnel, or a public reverse proxy are all valid as long as the public endpoint has trusted HTTPS and forwards WebSocket upgrades.
