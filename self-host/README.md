# Misty Self-hosted

This bundle runs one Misty API leader, PostgreSQL, and the Yjs collaboration service. Desktop clients connect through one HTTPS origin. The reverse proxy must send `/api/*` to the API and `/parties/*` WebSocket upgrades to the collaboration service.

## Install

1. Copy `.env.example` to `.env`, use the immutable image digests from a Misty release, and replace every placeholder secret.
2. Set `MISTY_PUBLIC_API_URL` to the externally reachable HTTPS URL ending in `/api`, `MISTY_COLLAB_PUBLIC_URL` to the same public origin, and `PARTYKIT_HOST` to its hostname. Loopback development may use an `http://127.0.0.1:PORT` collaboration origin; non-loopback deployments require HTTPS.
3. Choose `MISTY_LIBRARY_BACKEND=filesystem` for the bundled `library_data` volume, or `s3` and fill the generic S3 settings. The older `R2_*` names remain supported by the API.
4. Start the stack with `docker compose --env-file .env -f compose.yml up -d`.
5. Create the one-time administrator token:

   ```sh
   docker compose --env-file .env -f compose.yml run --rm --entrypoint misty-admin api bootstrap-token
   ```

6. In Misty Desktop, sign in to Misty Hosted with an eligible subscription or trial, open Settings → Advanced → Connection, select Self-hosted, and enter the complete server URL. After restart, create the first account with the bootstrap token.

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

Back up PostgreSQL and the Library backend before every update. Stop writes, take the database and blob backups together, replace both image references with the new release digests, run `docker compose pull`, then `docker compose up -d`. PostgreSQL stores account, Space, realtime, and compacted Yjs document state; `library_data` or the configured S3 bucket stores Library blobs.

Misty Desktop never installs, elects, starts, or fails over this leader. LAN DNS, a VPN, Cloudflare Tunnel, or a public reverse proxy are all valid as long as the public endpoint has trusted HTTPS and forwards WebSocket upgrades.
