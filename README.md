# Misty Server

Misty Server provides accounts, Spaces and file services for [Misty](https://github.com/misty-org/misty). Agents, model calls and web search run on each person's desktop: the server never calls a model, holds no search key and makes no search call.

This page sets up the development stack, which the [`misty` CLI](https://github.com/misty-org/misty-cli) runs. To run a server for yourself or other people, see [Self-hosting](#self-hosting).

## Development stack

### Prerequisites

- Git.
- Rust through rustup, to build the `misty` CLI with its pinned toolchain.
- Docker Engine with Compose v2.
- curl, which the CLI uses to call the Cloudflare API.
- A Cloudflare account with a domain, and an API token for that account and zone with account and zone read, Tunnel edit and DNS edit. See [Cloudflare's tunnel setup](https://developers.cloudflare.com/tunnel/get-started/).

Go, PostgreSQL, LiveKit and cloudflared run inside the containers. Live co-editing rooms run in the `misty-collab` service, a second binary from this repository's image, beside the API in compose, so they need nothing from Cloudflare; the tunnel only publishes the stack. Working on the desktop app needs its own [toolchain](https://github.com/misty-org/misty/blob/main/CONTRIBUTING.md#prerequisites).

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

`misty setup server` creates `.env/server/` in your Misty data folder (`~/.misty/dev/default` unless `~/.misty/misty.conf` names another) with development credentials and keys. Running it again keeps existing values. Keep these keys with your database and out of Git.

### Configure Cloudflare

Set `CLOUDFLARE_API_TOKEN` in your shell, or save it with `misty env set CLOUDFLARE_API_TOKEN`, which reads the value from stdin. Don't put credentials in command arguments.

Preview the tunnel and DNS route, then run the same command with `--apply` to create them and save the configuration:

```sh
misty setup cloudflare --account ACCOUNT_ID --zone ZONE_ID --hostname api.example.com
```

Setup reuses the named tunnel, keeps unrelated tunnel routes and stops on a conflicting DNS record.

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
misty doctor server
misty doctor cloudflare
```

`up` builds and starts the containers, applies migrations, waits until the services are healthy and returns. If startup fails, `up` prints the end of the relevant log. Build output, with secrets redacted, is saved in `.misty/logs/` here, and `--verbose` prints it after the build. Container logs rotate at 10 MB, three files per service.

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

The first account to register on a fresh server needs no invitation. Registering first doesn't make it the server's owner. The owner is the account that signs in with Google as `MISTY_OWNER_EMAIL` (set it in `misty.env`). Google sign-in proves the address, so that email can't register with a password, and a password account is never the owner. Without `MISTY_OWNER_EMAIL` the server has no owner. The owner sees the server's alerts in the app and is always let in past `MISTY_ALLOWED_EMAILS`.

Later accounts come from an open Space invitation to that email, or from the operator account API under `/operator/v1`, which is mounted only when `MISTY_OPERATOR_TOKEN` is set and takes it as a bearer token.

The API creates an account with limits on storage, shared Spaces and people per Space (unlimited unless set), plus whether the account is set up for use (`devices`: `0` means not yet, anything else means yes), and returns a single-use setup key with its sign-in link. Without Mailjet configured the server sends no email, so the caller passes the link on; the same link resets a forgotten password. The API also suspends and resumes accounts, and closes them: a closed account is read-only, then deleted with all its data after the given number of days unless it is resumed.

## Self-hosting

A server for real use runs from `compose.yml`, not the CLI. Its settings live in the root `~/.misty/misty.conf` names (`misty.root`, `~/.misty/rel/<version>` on a server), in that root's `.env/server/`, never in `~/.misty` itself. Compose can't read `misty.conf`, so `compose.yml` takes the folder from `MISTY_ENV_DIR` and stops when it is unset, for every `docker compose` command (export it in your shell's profile); the `misty` CLI sets it from `misty.conf`. You write three env files there:

| File | Values |
| --- | --- |
| `misty.env` | `MISTY_HOSTNAME`, plus any optional settings such as `MISTY_OWNER_EMAIL` |
| `cloudflare.env` | `CLOUDFLARE_API_TOKEN`. Setup finds the zone from `MISTY_HOSTNAME` and the account from the zone. `CLOUDFLARE_ZONE_ID` and `CLOUDFLARE_ACCOUNT_ID` are still read when set, and must match. |
| `google.env` | `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` |

On the first start the setup service generates one secret, `MISTY_SERVER_SECRET`, into the stack's `secrets` volume. Every key comes from it: session signing, the operator and metrics tokens, the database passwords and the backup key. Each key is derived with HKDF-SHA256 under its own label and version. The API gets only its own share, `MISTY_API_SECRET`. On every start setup also provisions the Cloudflare tunnel, DNS and R2 buckets, and the migrate service applies migrations. Live co-editing rooms run in the `misty-collab` service, from the same image as the API, beside it in `compose.yml`, with nothing to set up. Everything else follows from the hostname or comes from Cloudflare, so the secrets volume is the one thing to keep a copy of off the server.

Setup's other commands run with `docker compose run --rm setup <command>`:

- `rotate NAME…` gives a key its next version. The old value stays valid in its `*_PREVIOUS` slot until `retire NAME…`. `SPACE_LINK_ENCRYPTION_KEY` and `MISTY_BACKUP_AGE_IDENTITY` protect stored data, so they never rotate.
- `operator-token` prints the operator token, for a service that calls the operator API. Setup also writes it to `operator/token` in the secrets volume, which a service beside the server can mount and read. `backup-key` prints the backup key.
- `adopt-root` is for a secrets volume made before `MISTY_SERVER_SECRET`, which stores each key on its own. Such a volume keeps working unchanged, so nobody is signed out. `adopt-root` moves its keys onto the root, keeping each old value valid in its previous slot, so sessions survive. Afterwards, `retire` each key it moved. It leaves the two data-bound keys alone unless you pass `--discard-data`, which makes shared links and older backups unreadable. Use that only on a server with no data you need.

The API's `/health` reports the commit its image was built from. Published images have it built in. When you build on the server, pass it with `MISTY_SERVER_VERSION=$(git rev-parse HEAD) docker compose up -d --build`.

```sh
git clone https://github.com/misty-org/misty-server.git
cd misty-server
root=~/.misty/rel/1.0.0   # the misty-server version you run
mkdir -p -m 700 "$root/.env/server"
[ -e ~/.misty/misty.conf ] || printf '[metadata]\nmisty.manifest = v1\nmisty.root = %s\n' "$root" > ~/.misty/misty.conf
export MISTY_ENV_DIR="$root/.env/server"
# write misty.env, cloudflare.env and google.env in $MISTY_ENV_DIR
docker compose up -d --build
docker compose logs setup
```

Compose also reads three optional variables from where it runs (the shell or a `.env` file beside `compose.yml`, not `misty.env`):

- `MISTY_API_IMAGE` and `MISTY_TOOLS_IMAGE` run published images instead of building from source. Each push to `main` publishes `ghcr.io/misty-org/misty-server-api:<commit SHA>` and `ghcr.io/misty-org/misty-server-tools:<commit SHA>` (the full 40-character SHA), and each `v*` git tag publishes the same images tagged with its name, such as `v1.2.3`. Tags never move and there is no `latest`. Set both to the same commit, then run `docker compose up -d` without `--build`.
- `MISTY_TUNNEL_EXTRA_INGRESS` publishes more hostnames through the server's tunnel, for services you run beside it on the `edge` network: a comma-separated list of `host=service`, such as `billing.example.com=http://billing:8091`. Each host must be in the same zone as `MISTY_HOSTNAME`; setup routes it before the tunnel's catch-all and keeps its proxied DNS record. Empty by default. Removing an entry stops routing it but leaves its DNS record for you to delete.

The [self-hosting guide](https://docs.mistysys.com/self-hosting) lists every value and covers optional email, backups, secret rotation and the console.

## More

- [Backend architecture](https://github.com/misty-org/misty/wiki/Server-backend-architecture)
- [Server development](https://github.com/misty-org/misty/wiki/Server-development)
