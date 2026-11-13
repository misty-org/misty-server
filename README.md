# Misty Server

Misty Server provides accounts, Spaces and file services for [Misty](https://github.com/misty-org/misty). Agents, model calls and web search run on each person's desktop: the server never calls a model, holds no search key and makes no search call.

This page sets up the development stack, which the [`misty` CLI](https://github.com/misty-org/misty-cli) runs. To run a server for yourself or other people, see [Self-hosting](#self-hosting).

## Development stack

### Prerequisites

- Git.
- Rust through rustup, to build the `misty` CLI with its pinned toolchain.
- Docker Engine with Compose v2.
- curl, which the CLI uses to call the Cloudflare API.
- A Cloudflare account with a domain and a workers.dev subdomain turned on, and an API token for that account and zone with account and zone read, Tunnel edit, DNS edit and Workers Scripts edit. See [Cloudflare's tunnel setup](https://developers.cloudflare.com/tunnel/get-started/).

Go, Node.js, PostgreSQL, Wrangler and cloudflared run inside the containers. The development stack always uses Cloudflare; there is no local-only collaboration mode. Working on the desktop app needs its own [toolchain](https://github.com/misty-org/misty/blob/main/CONTRIBUTING.md#prerequisites).

### Install

The CLI finds this repository and the desktop app beside it, so keep all three checkouts in one folder:

```sh
git clone https://github.com/misty-org/misty.git
git clone https://github.com/misty-org/misty-server.git
git clone https://github.com/misty-org/misty-cli.git
cd misty-cli
cargo install --path . --locked
misty setup server
```

`misty setup server` creates `~/.misty/.env/server/` with development credentials and collaboration keys. Running it again keeps existing values. Keep these keys with your database and out of Git.

### Configure Cloudflare

Set `CLOUDFLARE_API_TOKEN` in your shell, or save it with `misty env set CLOUDFLARE_API_TOKEN`, which reads the value from stdin. Don't put credentials in command arguments.

Preview the tunnel and DNS route, then run the same command with `--apply` to create them and save the configuration:

```sh
misty setup cloudflare --account ACCOUNT_ID --zone ZONE_ID --hostname api.example.com
```

Setup reuses the named tunnel, keeps unrelated tunnel routes and stops on a conflicting DNS record. It doesn't deploy the Worker.

To see and check your settings:

```sh
misty env describe
misty env status
misty env check
```

`misty doctor server --fix` creates missing local files and development secrets. It doesn't touch Cloudflare.

### Start

```sh
misty server up
misty server deploy
misty doctor server
misty doctor cloudflare
```

`up` builds and starts the containers, applies migrations, waits until the services are healthy and returns. `deploy` deploys the development collaboration Worker; `up` never does. If startup fails, `up` prints the end of the relevant log. Build output, with secrets redacted, is saved in `.misty/logs/` here, and `--verbose` prints it after the build. Container logs rotate at 10 MB, three files per service.

```sh
misty server status
misty server logs api --tail 100
misty server logs api --follow
misty server down
```

Check `/health` on your public API hostname before connecting from another computer. The API itself answers only under `/v1`, so a production server refuses to start unless `MISTY_PUBLIC_API_URL` ends in `/v1`. A healthy service doesn't prove that accounts and Spaces work, so try them in the app too.

### Run the desktop app against it

```sh
misty setup desktop
misty doctor desktop
misty desktop dev
```

`misty desktop dev` points the app at this stack's API and website unless `MISTY_PUBLIC_API_URL` or `MISTY_PUBLIC_URL` is already set in your shell.

### Operator console

```sh
misty server up --gui
```

With `--gui`, `up` also opens the operator console in your browser. It listens only on `127.0.0.1:7070`, never through the tunnel, and a one-time link signs the browser in. Running the command again reuses a console that is already running; `misty server down` stops it.

The console shows health and traffic, the development services, customers (accounts with their storage against its limit, and their sessions), background jobs, configuration, database migrations, local backups and service logs. Traffic needs `MISTY_METRICS_TOKEN`. With `MISTY_OPERATOR_TOKEN` set, the Customers page can also set limits, suspend, close and resume accounts and issue sign-in links, through the operator account API. Each change is recorded in the audit log on the Overview.

The views are templ files in `internal/console`; run `make generate` after editing them.

## Accounts

The first account to register on a fresh server owns it. Later accounts come from an open Space invitation to that email, or from the operator account API under `/operator/v1`, which is mounted only when `MISTY_OPERATOR_TOKEN` is set and takes it as a bearer token.

The API creates an account with limits on storage, shared Spaces and people per Space (unlimited unless set), plus whether the account is set up for use (`devices`: `0` means not yet, anything else means yes), and returns a single-use setup key with its sign-in link. Without Mailjet configured the server sends no email, so the caller passes the link on; the same link resets a forgotten password. The API also suspends and resumes accounts, and closes them: a closed account is read-only, then deleted with all its data after the given number of days unless it is resumed.

## Self-hosting

A server for real use runs from `compose.yml`, not the CLI. You write three env files under `~/.misty/.env/server/` (`MISTY_ENV_DIR` names another folder). On every start the setup service generates missing secrets and provisions the Cloudflare tunnel, DNS, R2 buckets and Worker, and the migrate service applies migrations. The secrets live only in the stack's `secrets` volume, so keep a copy off the server.

```sh
git clone https://github.com/misty-org/misty-server.git
cd misty-server
mkdir -p -m 700 ~/.misty/.env/server
# write misty.env, cloudflare.env and google.env under ~/.misty/.env/server/
docker compose up -d --build
docker compose logs setup
```

The [self-hosting guide](https://docs.mistysys.com/self-hosting) lists every value and covers optional email, backups, secret rotation and the console.

## More

- [Backend architecture](https://github.com/misty-org/misty/wiki/Server-backend-architecture)
- [Server development](https://github.com/misty-org/misty/wiki/Server-development)
