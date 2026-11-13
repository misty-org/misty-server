# Misty server

The Go API and the Cloudflare Worker it uses (`services/journal-collab`). The desktop app, the
`misty` CLI are in [misty-org/misty](https://github.com/misty-org/misty); keep
that repository checked out beside this one, as `../misty`, because the CLI runs this one from there.
[README.md](README.md) sets up the development stack, and the rules that apply everywhere are in `../misty/AGENTS.md`.

# Rules

- Misty is open source under the AGPL-3.0-or-later (copyleft), with the attribution term in
  `NOTICE`. Add dependencies only under AGPL-compatible licenses (MIT, BSD, Apache-2.0, MPL-2.0,
  LGPL), never proprietary or non-commercial ones.
- Every feature is on for everyone. Cloudflare and Google sign-in are parts of every
  server. The one exception is email: with Mailjet set up, the server sends sign-in links,
  invitations and closing notices; without it, it hands out the links.
- There is no billing here. Misty Pro is just another Misty server; its Stripe side lives in the
  private misty-cloud service and reaches this server only through the operator account API.
  Never add prices, plan gates or payment code; per-account limits are generic and unlimited unless
  set.
- Nothing AI runs on the server. An agent run starts, runs and ends on the person's computer, with
  its model calls, web search, memory, workflows, schedules, chats and connected apps. The server
  sees only what it would see from the person using the app by hand: a note saved, a chat message
  sent, a file uploaded. A Space lists an agent as a name under its person, with no row behind it.
  Never add a route that takes a prompt, a tool result or a model's output
  (`test/contract/http/app/no_agents_on_server_test.go`). The server can read what it stores: Space
  notes, chats and files.
- No `*.env` file is ever committed, not even an example.
- Many Go files use CRLF line endings. Scripted edits must preserve them.

# Checks

Run `git config core.hooksPath .githooks` once. Commits and pushes run the checks in
`.githooks/checks.sh`, and `.githooks/server-tests.sh` runs the Go suite against a disposable
PostgreSQL container.

# Commits

Commit messages and pull request descriptions never carry a `Co-Authored-By:` trailer or any other
AI attribution line. Commits are authored by the person alone.
