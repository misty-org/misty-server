# Odysseys

The Odysseys benchmark ([paper](https://arxiv.org/abs/2604.24964), [tasks](https://github.com/ljang0/Odysseys), MIT) run through Misty's product path, for [docs/plans/odysseys-campaign.md](../../../../../docs/plans/odysseys-campaign.md). Odysseys has 200 long, multi-site tasks from real browsing, each with 3 to 12 rubric items; the headline score is the share of tasks where every rubric item passes ("perfect").

Each task is one composer request on the local stack, with this computer as the device, so the agent does what it does for a person: search, fetch pages, and open its own browser tab, which joins the run. The recorder pictures the agent's tabs (and tabs it opened for the person) after every browser step and once at the end, through the desktop's debug bridge, without showing them. `judge.ts` is Odysseys' per-rubric judge with its prompt unchanged: one call per rubric item, from the action history and every screenshot.

Differences from the paper's setup, kept on purpose:

- The agent is Misty's product (its tools, its own tab, web search and fetch), not a screenshot-only computer-use loop in a VM.
- The judge runs on a Claude model on Misty's own key (`MISTY_ODYSSEYS_JUDGE`), not Gemini 3.1 Flash-Lite, and sees at most the last 24 screenshots.
- The browser profile has no saved sign-ins (`MISTY_EVAL_EPHEMERAL_PAGES=1` on the desktop).

## Splits

`tasks.ts` fixes the splits by a stable hash of each task id, per level: **dev** (40: 9 easy, 9 medium, 22 hard) for every iteration, **holdout** (60) for milestones, **rest** (100) only in full passes.

## Run

1. Start the stack (`misty server up`) with `MISTY_ALLOW_MODEL_OVERRIDE=1` and the Anthropic key in the dev env files.
2. Start the desktop app in development with the eval bridge and a throwaway page session:

   ```bash
   MISTY_BROWSER_EVAL_BRIDGE_PORT=47123 MISTY_BROWSER_EVAL_BRIDGE_TOKEN=<24+ chars> MISTY_EVAL_EPHEMERAL_PAGES=1 npm run tauri dev
   ```

3. From `server/apps/agent-runtime`:

   ```bash
   set -a; . ../../.env/dev/integrations/ai.env; set +a
   MISTY_ODYSSEYS=1 MISTY_BROWSER_EVAL_BRIDGE=http://127.0.0.1:47123 MISTY_BROWSER_EVAL_BRIDGE_TOKEN=<token> \
     MISTY_WORKFLOW_EVAL_DEVICE=<device_…> MISTY_WORKFLOW_EVAL_DEVICE_API_BASE=https://dev-api.mistysys.com/v1 \
     npm run eval:odysseys
   ```

| Variable | Meaning |
|---|---|
| `MISTY_ODYSSEYS_SPLIT` | `dev` (default), `holdout`, `rest` or `all` |
| `MISTY_ODYSSEYS_TASKS` | Task ids or id prefixes, instead of a split |
| `MISTY_ODYSSEYS_LIMIT`, `_OFFSET` | How many tasks, from where (default 10 from 0) |
| `MISTY_ODYSSEYS_MODEL`, `_EFFORT` | The agent's model and effort (Haiku 5.5, `low`) |
| `MISTY_ODYSSEYS_JUDGE` | The judge model (Haiku 5.5) |
| `MISTY_ODYSSEYS_EARLY_STOP` | `1` (default) stops when 4 of the first 6 tasks fail |
| `MISTY_ODYSSEYS_CAP` | The campaign's spend cap in dollars (50), kept across passes in `reports/ledger.json` |

Each pass writes `reports/<time>/report.json` and, per task, a folder with `steps.jsonl`, the screenshots and `result.json` (the judge's reasoning per rubric item).
