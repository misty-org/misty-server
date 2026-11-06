# Misty server

The Go API and the Cloudflare Worker it uses (`services/journal-collab`). The desktop app, the
`misty` CLI and the product docs are in [misty-org/misty](https://github.com/misty-org/misty); keep
that repository checked out beside this one, as `../misty`, because the CLI runs this one from there.
[README.md](README.md) sets up the development stack and `internal/README.md` maps the packages.
How features work is in `../misty/docs/`, which `../misty/AGENTS.md` indexes; change those docs in
the same piece of work as the behavior.

# Rules

- Misty is open source under the AGPL-3.0-or-later (copyleft), with the attribution term in
  `NOTICE`. Add dependencies only under AGPL-compatible licenses (MIT, BSD, Apache-2.0, MPL-2.0,
  LGPL), never proprietary or non-commercial ones.
- Every feature is on for everyone. Cloudflare, Composio and Google sign-in are parts of every
  server. The one exception is email: with Mailjet set up, the server sends sign-in links,
  invitations and closing notices; without it, it hands out the links.
- There is no billing here. Misty Pro is just another Misty server; its Stripe side lives in the
  private misty-billing service and reaches this server only through the operator account API.
  Never add prices, plan gates or payment code; per-account limits are generic and unlimited unless
  set.
- Agents, every model call and web search run on the person's computer. Never send a model key,
  search key, model call or search to the server, store them or log them.
- Conversations are stored here, not encrypted, per person per Space, including tool output, with
  screenshots in the object store. A tool result marked redacted stays sealed and is never stored
  readable. Only the vault is protected from the server.
- No `*.env` file is ever committed, not even an example.
- Many Go files use CRLF line endings. Scripted edits must preserve them.

# Checks

Run `git config core.hooksPath .githooks` once. Commits and pushes run the checks in
`.githooks/checks.sh`, and `.githooks/server-tests.sh` runs the Go suite against a disposable
PostgreSQL container.

# Commits

Commit messages and pull request descriptions never carry a `Co-Authored-By:` trailer or any other
AI attribution line. Commits are authored by the person alone.
