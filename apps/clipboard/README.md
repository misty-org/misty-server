# Clipboard Worker

Carries the cloud clipboard between a person's admitted Misty devices (see [docs/design/clipboard/BRIEF.md](../../../docs/design/clipboard/BRIEF.md)). Clips are encrypted on the device with a key derived from the vault root. This Worker only ever stores and serves ciphertext.

- `ClipboardRoom` is a SQLite Durable Object, one per account. It keeps the 20 newest clips for 24 hours, enforces 25 MB per clip and 200 MB uploaded per day, and pushes new clips to connected devices over hibernating WebSockets.
- Blobs live in the `misty-clipboard` R2 bucket, addressed by the SHA-256 of their ciphertext. Downloads are served from the edge cache when possible.
- Every request needs a five-minute ticket from `POST /v1/devices/{deviceID}/clipboard-ticket` on the Misty API, signed with `CLIPBOARD_TICKET_PRIVATE_KEY`. The Worker holds only the public key.

## Configure

The API needs `MISTY_CLIPBOARD_HOST` (the Worker's hostname), `CLIPBOARD_TICKET_PRIVATE_KEY` (PKCS#8 Ed25519, like the journal ticket key) and `CLIPBOARD_ROOM_SALT` (32+ random bytes, base64). Without `MISTY_CLIPBOARD_HOST` the ticket route is not mounted and devices keep the LAN clipboard only.

Deploy the Worker with the matching public key:

```sh
CLIPBOARD_TICKET_PUBLIC_KEY=<base64 raw key> scripts/cloudflare-setup.sh
```

## Develop

```sh
npm ci
npm run typecheck
npm test
npm run dev
```
