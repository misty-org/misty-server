# Odysseys

The Odysseys benchmark ([paper](https://arxiv.org/abs/2604.24964), [tasks](https://github.com/ljang0/Odysseys), MIT) run through Misty's product path, for [docs/agents/evals/odysseys.md](../../../../../docs/agents/evals/odysseys.md). Odysseys has 200 long, multi-site tasks from real browsing, each with 3 to 12 rubric items; the headline score is the share of tasks where every rubric item passes ("perfect").

Each task is one composer request on the local stack, with this computer as the device, so the agent does what it does for a person: search, fetch pages, and open its own browser tab, which joins the run. The recorder pictures the agent's tabs (and tabs it opened for the person) after every browser step and once at the end, through the desktop's debug bridge, without showing them. `judge.ts` is Odysseys' per-rubric judge with its prompt unchanged: one call per rubric item, from the action history and every screenshot.

Differences from the paper's setup, kept on purpose:

- The agent is Misty's product (its tools, its own tab, web search and fetch), not a screenshot-only computer-use loop in a VM.
- The judge runs on a Claude model on the instance's Anthropic key (`MISTY_ODYSSEYS_JUDGE`), not Gemini 3.1 Flash-Lite, and sees at most the last 60 screenshots (unchanged pages are pictured once).
- The browser profile has no saved sign-ins (`MISTY_EVAL_EPHEMERAL_PAGES=1` on the desktop).

Without paid credit, the agent and the judge run on NVIDIA's free hosted models (`nim/…`); see [docs/agents/evals/free-model.md](../../../../../docs/agents/evals/free-model.md). Scores on a free model compare only with other passes on the same model.

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
| `MISTY_ODYSSEYS_TURNS` | Model turns per task (200, the most a dev eval may ask for); the dev stack honors it only with `MISTY_ALLOW_MODEL_OVERRIDE`, which also gives the run four hours of execution |
| `MISTY_ODYSSEYS_JUDGE` | The judge model (Haiku 5.5) |
| `MISTY_ODYSSEYS_WATCH_MINUTES` | How often the progress watch (`watchdog.ts`) looks at a run (3). No clock ends a run: it is stopped only after two quiet looks or two looks with nothing but repeated results, and still graded |
| `MISTY_ODYSSEYS_TASK_MINUTES` | An optional hard cap per task; unset, there is none |
| `MISTY_ODYSSEYS_JUDGE_LANES` | Rubrics judged at once on a free (`nim/`) judge (5) |
| `MISTY_ODYSSEYS_REGRADE` | A task folder to judge again from its saved steps and pictures, without running it |
| `MISTY_ODYSSEYS_RESUME` | A task folder from an earlier pass (`reports/<time>/<task>`): the task continues in that run's conversation from where it stopped, and is graded on both runs' steps |
| `MISTY_ODYSSEYS_RESUME_FAILED` | An earlier pass folder (`reports/<time>`): only its tasks that did not pass go on, each from its own checkpoint with the parts that passed kept (the agent's ledger); passed tasks count as carried. For iterating on fixes: when all pass, `GATE:` prints and `reports/gate.json` is written, then run the full batch once for the score that counts, and now and then after |
| `MISTY_ODYSSEYS_EARLY_STOP` | `1` (default) stops when 4 of the first 6 tasks fail |
| `MISTY_ODYSSEYS_CAP` | The campaign's spend cap in dollars (50), kept across passes in `reports/ledger.json` |

Each run prints `RUN ENDED <task> <terminal> …` the moment the agent stops, before grading (a run that did no work is not graded). Each pass writes `reports/<time>/report.json` and, per task, a folder with `steps.jsonl`, the screenshots and `result.json` (the judge's reasoning per rubric item).

## BU Bench V1

The same runner also takes [BU Bench V1](https://github.com/browser-use/benchmark) (100 tasks: 20 each from WebBench, Online-Mind2Web 2, interaction tests, GAIA and BrowseComp), set by `MISTY_ODYSSEYS_DATASET=bu-v1` and `MISTY_BU_BENCH_DIR=<a local checkout of browser-use/benchmark>`. Its tasks ship encrypted so their text stays out of crawlers and training data: `bu-bench.ts` decrypts them in memory and nothing of them is committed here (reports are git-ignored). Each task gets BU's own binary judge, with the system prompt read from the checkout's `judge.py` at run time, on the last ten distinct pictures. BU has no levels; the runner groups lookups and interaction tests as easy, Online-Mind2Web 2 as medium, GAIA and BrowseComp as hard. Select tasks with `MISTY_ODYSSEYS_TASKS` (BU's task ids) or `MISTY_ODYSSEYS_SPLIT=all` with a limit and offset.
