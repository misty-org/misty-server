# Browser benchmark

Phase 0 of [docs/plans/agentic-browser.md](../../../../../docs/plans/agentic-browser.md). It measures how well and how fast an agent finishes real browser work: forms, spreadsheets, documents, work apps, mail, research and long tasks.

Each task runs a real model with the same browser tools, descriptions, guidance and task-completion contract production runs get, in a tab of Misty's own browser: the system webview (WKWebView on macOS, WebView2 on Windows). Every tool call goes through the desktop's agent path, as a device job does: a task lease, a grant for the tab and `browser_agent_execute` with its page layer, site checks and input rules. Tabs are hidden, as agents' own tabs are, so clicks arrive as page events; set `MISTY_BROWSER_EVAL_VISIBLE=1` to watch them, which also uses native input on macOS.

The desktop app reaches the benchmark through a bridge that exists only in debug builds (`src-tauri/src/infra/browser_eval_bridge.rs`). It listens on 127.0.0.1, answers only requests with its token, and uses the browser's normal profile: live tasks see the accounts you signed in to in Misty. Use test accounts there.

- `catalog.json` is generated from the Go descriptors. `TestBrowserEvalCatalogIsCurrent` fails when it drifts. Regenerate it with `MISTY_WRITE_BROWSER_EVAL_CATALOG=1 go test ./internal/platform/httpapi -run TestBrowserEvalCatalogIsCurrent` from `server/`.
- `page-tools.ts` passes each tool to the desktop unchanged; only `browser_extract` makes its extraction model call here, as the server does. `test/browser-page-tools.test.ts` drives every fixture flow through Misty's browser without a model; it skips when the bridge isn't running.
- `browser_act` is not in the eval: its screen agent runs on the desktop. `src/features/agents/screenAct/screenAct.acceptance.ts` covers it.

## Run

Start the desktop app in development with the bridge on, from the repository root. Pick any free port and a random token of 24 characters or more:

```bash
export MISTY_BROWSER_EVAL_BRIDGE_PORT=47123 MISTY_BROWSER_EVAL_BRIDGE_TOKEN=$(openssl rand -hex 24)
npm run tauri dev
```

Then, from `server/apps/agent-runtime`, with the same token:

```bash
set -a; . ../../.env/dev/integrations/ai.env; set +a
export MISTY_BROWSER_EVAL_BRIDGE=http://127.0.0.1:47123 MISTY_BROWSER_EVAL_BRIDGE_TOKEN=<the token>
MISTY_BROWSER_EVAL_MODELS=openai/gpt-6-luna npm run eval:browser
```

Models run on Misty's keys through the runtime's own model routing.

| Variable | Meaning |
|---|---|
| `MISTY_BROWSER_EVAL_MODELS` | Comma-separated models to compare. Required. |
| `MISTY_BROWSER_EVAL_JUDGE` | The rubric judge's model. Defaults to the first model. |
| `MISTY_BROWSER_EVAL_TASKS` | Task ids, categories or apps to run. Defaults to all that can run. |
| `MISTY_BROWSER_EVAL_REASONING` | Reasoning effort. Defaults to `low`. |
| `MISTY_BROWSER_EVAL_MAX_OUTPUT` | Output cap per model call. Defaults to production's. |
| `MISTY_BROWSER_EVAL_BRIDGE`, `MISTY_BROWSER_EVAL_BRIDGE_TOKEN` | The running desktop's bridge address and token. Required. |
| `MISTY_BROWSER_EVAL_VISIBLE` | `1` shows each task's tab instead of keeping it hidden. |
| `MISTY_BROWSER_EVAL_ACCOUNTS` | Account tags you signed in to in Misty's browser, such as `google,microsoft,notion`. Use test accounts. |
| `MISTY_EVAL_*_URL` | Start pages for live tasks, named in `tasks.json`. |

Each run writes `reports/report-<time>.json` with every run's verdicts, time, model turns, tool calls, the share of time spent in tools and token use, plus a summary per model and per category.

## Tasks

`tasks.json` holds 39 tasks:

- **17 fixture tasks.** They run on the local pages in `sites/` and need no account, so every run of them is comparable. They cover a multi-step signup with a custom checkbox and a retry after a server error, an autocomplete and date picker, drafting versus sending mail, a paged table, a document editor, a spreadsheet grid, a board with menus, a cookie banner covering a button, a virtualized list, a confirm dialog, a cited search, a two-site task and two prompt injections.
- **22 live tasks.** They cover Google Sheets, Docs, Forms and Classroom, Excel, Word and Microsoft Forms, Airtable, Notion, Typeform, HubSpot, Jira, Linear, Trello, Gmail, Outlook, Slack, Canvas, live web research and GitHub to Linear. Each needs a test account signed in to in Misty's browser and a prepared start page.

A task passes when every verdict it has passes:
- `check` is an expression run in the page after the agent finishes;
- `expect` lists strings the final answer must contain;
- `rubric` lists criteria a judge model grades. The judge sees the final screenshot, the page text, the answer, the tool calls and the newest read of each page the agent looked at.

Injection tasks also count how often the agent did what the page told it to.

In the eval there is no person to answer approval cards, so consequential steps the agent marks `allowConsequential` run as if the account does not ask first.

## Trace a real run

For runs through the full stack (desktop, API, runtime), `npm run trace -- <invocation id>` prints a waterfall from the local database and the runtime's `tool_timing` log lines. It shows model turns, each tool call with its token-exchange and MCP time, and each device job's queue, claim and run time.
