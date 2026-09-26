# Misty Server

Misty Server provides accounts, encrypted workspace sync, file services, and the agent runtime. It lives in `server/` alongside the desktop app and CLI.

Set up the backend before the [desktop app](../README.md#get-started).

## Get started

### 1. Prerequisites

- Git and access to this repository.
- Rust/Cargo through rustup, to build the CLI using the pinned toolchain.
- Docker Engine with Compose v2 and Linux containers enabled.
- curl, for Cloudflare API setup.
- A Cloudflare account with a domain and a workers.dev subdomain enabled. Create an API token with access to the selected account and zone: account/zone read, Tunnel edit, DNS edit, and Workers Scripts edit. See [Cloudflare's tunnel setup](https://developers.cloudflare.com/tunnel/get-started/).

Go, Node.js, PostgreSQL, Redis, Wrangler, and cloudflared run inside the containers. Desktop development needs its own [native toolchain](../README.md#1-prerequisites). The full development stack still uses Cloudflare; a complete local-only collaboration mode is not implemented.

### 2. Installation

```sh
git clone https://github.com/misty-org/misty.git
cd misty
cargo install --path cli --locked
misty setup server
```

Setup creates `server/.env/dev/` and generates development credentials and collaboration keys. Repeating it preserves existing values. Keep these keys with your database and out of Git.

### 3. Environment setup

Set `CLOUDFLARE_API_TOKEN` in your shell or save it through `misty env set dev CLOUDFLARE_API_TOKEN`, which reads its value from stdin. Do not put credentials in command arguments.

Preview your infrastructure configuration:

```sh
misty setup cloudflare --account ACCOUNT_ID --zone ZONE_ID --hostname api.example.com
```

Repeat with `--apply` to provision the tunnel and DNS route and save the configuration. Setup reuses the named tunnel and rejects conflicting DNS records. It preserves unrelated tunnel routes. It does not deploy the Worker until explicitly requested.

Inspect available settings and validate configuration:

```sh
misty env describe
misty env status dev
misty env check dev
```

Provider integrations require their own credentials. See the [environment guide](deploy/README.md). `misty doctor server --fix` can create missing local files and development secrets; it does not provision remote infrastructure.

### 4. Start and verify

```sh
misty server up
misty server deploy
misty doctor server
misty doctor cloudflare
```

`up` builds and starts containers, applies migrations, waits for service health, and returns. It does not redeploy the Worker. `deploy` explicitly deploys the development collaboration Worker.

```sh
misty server status
misty server logs api --tail 100
misty server logs api --follow
```

Normal startup shows progress and service status. Failures show the relevant diagnostic tail. Bounded, redacted build diagnostics are saved under `server/.misty/logs/`; `--verbose` displays that output after the build. Container logs rotate at 10 MB with three files per service.

Check `/health` on the configured public API origin before using another computer. Service health is not a substitute for testing account, sync, or agent workflows.

Then return to the desktop setup:

```sh
misty setup desktop
misty doctor desktop
misty desktop dev
```

Desktop setup creates a root `.env` pointing at the local API if that file is absent. Existing configuration is preserved. Set `MISTY_PUBLIC_API_URL` to your HTTPS API URL ending in `/v1` for remote devices.

## Development and deployment

- [CLI commands](../cli/README.md)
- [Environment configuration and production deployment](deploy/README.md)
- [Self-hosted deployment bundle](self-host/README.md)
- [Backend architecture](https://github.com/misty-org/misty/wiki/Server-backend-architecture)
- [Server development](https://github.com/misty-org/misty/wiki/Server-development)
- [Repository migration and validation ledger](https://github.com/misty-org/misty/wiki/Cli-server-consolidation)
