# Misty self-hosted server

This stack runs the browser API, PostgreSQL, Yjs collaboration, and the multi-step Agent runtime. Automation services and app installation services are retired. Existing automation volumes are not deleted by an upgrade; export or archive them before removing volumes manually.

Use immutable release image digests. Configure the API origin and WebSocket reverse proxy, database credentials, authentication signing keys, Library storage, collaboration keys, and your own AI provider credentials. Start with `docker compose --env-file .env -f compose.yml up -d`.

Create the first administrator bootstrap token with:

```sh
docker compose --env-file .env -f compose.yml run --rm --entrypoint misty-admin api bootstrap-token
```

Enrollment invitations and Space membership remain separate. Keep account authentication and membership checks enabled.

## Billing migration status

The optional billing adapter contract is documented in the [wiki](https://github.com/misty-org/misty/wiki/Server-billing-adapter). The current Go billing paths and self-host enrollment entitlement checks have not yet completed their cutover. Do not treat this intermediate checkout as an independently self-hostable release until the migration acceptance checklist is complete.

## Recovery

Reset a password through `misty-admin reset-password --email person@example.com`, supplying the new password on standard input. Disable an account and revoke its sessions through `misty-admin disable-account --email person@example.com`.

Back up PostgreSQL and Library blobs together before updates. Keep all signing/encryption keys stable across restarts. Do not remove archived application data merely because its old routes have been retired.

The desktop does not elect or start this server. LAN DNS, a VPN, a tunnel, or a reverse proxy may expose it, provided HTTPS is trusted and WebSocket upgrades are forwarded.
