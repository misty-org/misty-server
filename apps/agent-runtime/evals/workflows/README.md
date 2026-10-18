# Workflow evals

Phase 0 of [docs/plans/agent-harness.md](../../../../../docs/plans/agent-harness.md). The browser
suite starts every task inside a tab with only browser tools, so it measures how well the agent
browses. This suite starts where a person does (the composer, the cursor or a schedule)
with the full harness, so it also measures whether the agent picked the right route:
answer directly, search, read a page in the background, use a connected app, switch to the
browser, or take over the desktop. Every run starts in `agent.tool.answer`, so an `agent_tool_use` call
in the trace shows when the agent decided it needed more.

Tasks are in `tasks.json`. Those marked `"source": "polar"` follow the knowledge work Polar's
customers describe publicly (prospect research from a sheet, ICP checks, CRM updates, candidate
sourcing, competitor monitoring, inventory and order follow-ups, scheduled runs); the rest are
Misty's own surfaces.

The tasks follow [docs/plans/agent-harness.md](../../../../../docs/plans/agent-harness.md)
revision 4: no router, `agent.tool.answer` first, teaching as answer at `agent.entry.cursor`,
screenshots of every display on every cursor request, and dotted agent names that never use
UI labels.

## Scoring

Each run records every tool call and every run started for the request. A task passes when:

- every `expect` string appears in the final answer and the judge passes every `rubric` line;
- each `tools.required` tool was called, and no `tools.forbidden` tool was (`browser_*` matches
  the family);
- tool calls are at most `tools.maxCalls` and runs at most `tools.maxRuns`.

The report keeps answer quality and routing apart, so a correct answer reached the long way
shows as a routing failure, not a pass. It also records model turns, tokens, cost and wall time
per task, and compares models side by side like the browser suite.

Every task runs `MISTY_WORKFLOW_EVAL_REPEAT` times (3 by default), because one run can't tell an
improvement from luck. The report gives each model, category and task a pass rate and the spread
(least, median, most) of tool calls and seconds, and the pass ends with a table of them.

Each run's model turns and tokens come from the local billing ledger, which settles every turn
with its measured input, cached-input and output tokens. The report shows the share of input
billed at the cached price and what caching saved in dollars, priced from the AI Gateway's model
list, so a change that breaks prompt caching shows up as cost.

A relabel run repeats the suite with every UI label in the agent's glossary replaced by an
invented word (Spaces becomes, say, "Orbits", and the prompts say "my Orbit"). Routing must
score the same; a drop means a tool name, description or prompt still leans on a UI label.

Cold and warm runs measure learning. Each task's workflow signature is its category, app and
the shape of its inputs. A cold run starts with no `memory.workflow` for that signature; after a
successful run writes one, a warm run repeats the task with fresh inputs (different companies,
candidates or products, so the stored steps can't just replay an answer). The report shows each
workflow's learning curve: success, tool calls, model turns and cost, cold against warm.

## Run

The runner (`workflow.eval.ts`) sends each task through the local stack's real API, as the
composer or the cursor would, and reads the run's events: tool calls, screen hand-offs and the
answer. It refuses any API that isn't local. From `server/apps/agent-runtime`:

```bash
set -a; . ../../.env/dev/integrations/ai.env; set +a
MISTY_WORKFLOW_EVAL=1 npm run eval:workflows
```

| Variable | Meaning |
|---|---|
| `MISTY_WORKFLOW_EVAL` | `1` to run; the suite never runs with the unit tests. |
| `MISTY_ALLOW_MODEL_OVERRIDE` | Set on the local API (`1`) so the runner's per-model requests take effect. Everyone else runs the house model and effort, and production ignores the switch. |
| `MISTY_WORKFLOW_EVAL_MODELS` | Models to compare. Defaults to `anthropic/claude-haiku-5.5`, `openai/gpt-6-luna`, `google/gemini-3.5-flash-lite`. |
| `MISTY_WORKFLOW_EVAL_EFFORT` | Reasoning effort for every run (`low` by default; `medium`, `high`…). Needs `MISTY_ALLOW_MODEL_OVERRIDE` on the local API. |
| `MISTY_WORKFLOW_EVAL_JUDGE` | The rubric judge. Defaults to `anthropic/claude-haiku-5.5`. |
| `MISTY_WORKFLOW_EVAL_TASKS` | Task ids, categories or sources (`polar`, `misty`) to run. |
| `MISTY_WORKFLOW_EVAL_REPEAT` | Runs per task, 1 to 10. Defaults to 3. |
| `MISTY_BILLING_DB_CONTAINER` | The local billing database's container, for token usage. Defaults to `misty-billing-postgres-1`. |
| `MISTY_WORKFLOW_EVAL_BUDGET` | Dollars of AI Gateway spend before the pass stops. Defaults to 7.50. |
| `MISTY_WORKFLOW_EVAL_ACCOUNTS` | Connected test accounts, such as `gmail,google-calendar,github,slack,linkedin-signed-in`. Tasks needing others are skipped. |
| `MISTY_WORKFLOW_EVAL_DEVICE` | Your desktop's agent-device id (`trusted_devices.id`, `device_…`). The desktop dev app must run signed in to your account with the eval bridge on (`MISTY_BROWSER_EVAL_BRIDGE_PORT` and `_TOKEN`, as in the browser suite); the runner asks it to sign the workspace grant a chat from it would carry, so device tools such as web.fetch run on it. Also set `MISTY_BROWSER_EVAL_BRIDGE` and `MISTY_BROWSER_EVAL_BRIDGE_TOKEN`, and `MISTY_WORKFLOW_EVAL_DEVICE_API_BASE` when the app signed in through another address (such as `https://dev-api.mistysys.com/v1`). |
| `MISTY_ACCEPTANCE_API` | The local API. Defaults to `http://127.0.0.1:8081`. |

- **Budget.** The cap is read from the AI Gateway's own credit balance before every task, so
  model, search and judge calls all count. Per-task costs come from balance differences and
  can lag a little; the pass total is exact.
- **Your account.** Runs go to your own account on the local stack, so they use your memory,
  spaces and connected apps and show up in your conversations, as real runs would. Save your
  local sign-in once, without it reaching your shell history; the file is git-ignored:

  ```bash
  python3 -c 'import getpass,json,os; e=input("Email: "); p=getpass.getpass(); os.makedirs(".misty/acceptance",exist_ok=True); f=os.open(".misty/acceptance/workflow-eval-signin.json",os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600); os.write(f,json.dumps({"email":e,"password":p}).encode())'
  ```

  Run it from the repository root, or set `MISTY_WORKFLOW_EVAL_SIGNIN` to another file. A pass
  stops after three runs in a row are refused before the model runs (an allowance limit or a
  broken stack).
- **Seed data.** `npx jiti evals/workflows/seed.ts` fills your connected test apps through
  Misty's own Composio session: mail inserted into Gmail (nothing is sent; all labeled
  `Misty eval`), interview and busy events in Calendar (no invites go out), a private
  `misty-eval-sandbox` repo, a `#marketing-drafts` Slack channel and a "Weekly sync" note.
  Mail is skipped when already there. Gmail only marks really sent mail as Sent, so the seeded
  thank-you to Priya is found by sender and recipient, not in the Sent folder.
- **End state.** Account tasks list `state` checks: after the run, `state.ts` reads back what
  the task should have changed (issues, Gmail drafts and Sent, calendar events, a Slack
  channel, Misty items) through the same Composio session, keeps the fields a judge can check,
  and gives them to the judge as the evidence. Placeholders such as `{{github_owner}}`,
  `{{tomorrow_start}}` and `{{slack_channel:marketing-drafts}}` are filled in at run time.
- **Scoped and undone.** A check with `since` shows only items created or changed during the
  run. A check with `undo` removes what the run created once it is graded: drafts, events
  and Slack messages are deleted, Misty tasks archived and sandbox issues closed. Older items
  are never touched, and `skip` leaves another app's items (Reclaim events, join messages).
- **What the agent read.** With `MISTY_AI_TOOL_RESULT_EVENTS=1` in `server/.env/dev/runtime.env`,
  the local server adds each tool's raw result to its `tool.detail` event (never in
  production), and the judge reads those instead of the result titles alone.
- **Screen hand-offs.** Without a device, a run that asks for a screen ends there: its route
  is scored, its answer isn't.
- **Schedules** run as composer requests until the runner can claim a scheduled run.
- **Fixture screens** are HTML pages in `fixtures/screens/`, rendered with
  `fixtures/screens/render.swift` and scaled to 1280 x 800.

## Not built yet

- A fixture search index, so fixture research tasks don't depend on live engines;
  `research-cited-fixture` is skipped until it exists.
- Resume fixtures and attachment upload for `candidate-screen-rubric`.
- `web_fetch` (plan phase 1) and `agent.tool.use` (phase 2). Required tools that don't exist yet
  are reported as unavailable, not failed; tasks that need them to answer will fail, which is
  part of the baseline.
