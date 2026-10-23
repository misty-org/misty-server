// The pass's settings, from MISTY_ODYSSEYS_* variables (see README.md).
import { join, resolve } from "node:path";
import { Ledger } from "./spend.js";

export const env = (name: string, fallback = "") => process.env[name]?.trim() || fallback;
export const list = (value: string) =>
  value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
export const model = env("MISTY_ODYSSEYS_MODEL", "anthropic/claude-haiku-5.5");
export const effort = env("MISTY_ODYSSEYS_EFFORT", "low");
// How-to prompt rules to leave out of the agent's system prompt (docs/plans/agent-browser-actions.md,
// phase 0), by rule id, comma separated. The dev stack honors it only where a request may choose its
// model. Each result records which were off, so a score always says what prompt produced it.
export const promptOff = list(env("MISTY_ODYSSEYS_PROMPT_OFF"));
// Model turns per task: 200, the most a dev eval may ask for, as the leaders run 100 to 200.
export const turns = Number(env("MISTY_ODYSSEYS_TURNS", "200"));
export const judgeModel = env("MISTY_ODYSSEYS_JUDGE", "anthropic/claude-haiku-5.5");
export const split = env("MISTY_ODYSSEYS_SPLIT", "dev");
export const limit = Number(env("MISTY_ODYSSEYS_LIMIT", "10"));
export const offset = Number(env("MISTY_ODYSSEYS_OFFSET", "0"));
export const earlyStop = env("MISTY_ODYSSEYS_EARLY_STOP", "1") === "1";
// No clock ends a run: Odysseys tasks are long on purpose. The progress watch (watchdog.ts)
// looks every few minutes and stops only a run that went quiet or only repeats itself.
// MISTY_ODYSSEYS_TASK_MINUTES still sets a hard cap when one is wanted; past it the pass stops.
export const taskMs = env("MISTY_ODYSSEYS_TASK_MINUTES") ? Number(env("MISTY_ODYSSEYS_TASK_MINUTES")) * 60_000 : Infinity;
export const watchMs = Number(env("MISTY_ODYSSEYS_WATCH_MINUTES", "3")) * 60_000;
export const parallel = Number(env("MISTY_ODYSSEYS_PARALLEL", "3"));
export const here = resolve(import.meta.dirname);
export const ledger = new Ledger(
  join(here, "reports", "ledger.json"),
  Number(env("MISTY_ODYSSEYS_CAP", "50")),
);
// Checkpoints: MISTY_ODYSSEYS_RESUME names a task's folder from an earlier pass. That task goes
// on in the same conversation from where it stopped (after a fix, instead of starting over): the
// request and its last answer are the chat's history, the new turn asks for what remains, and the
// judge grades both runs' steps together as one trajectory.
export const resumeDir = env("MISTY_ODYSSEYS_RESUME");
// MISTY_ODYSSEYS_RESUME_FAILED names an earlier pass's folder (reports/<time>): only its tasks that
// did not pass go on, each from its own checkpoint, with the parts that passed kept (the ledger,
// docs/agents/checkpoints.md). The passed tasks count in the summary as carried. A resumed
// pass is for iterating on fixes; the number to report is a full pass, run once every task has
// passed in resumed form (the gate printed at the end), then now and then.
export const resumeFailedDir = env("MISTY_ODYSSEYS_RESUME_FAILED");
// MISTY_ODYSSEYS_REGRADE names a task's folder to judge again from its saved steps and pictures,
// without running it (after a judge fix, or a verdict the judge never gave).
export const regradeDir = env("MISTY_ODYSSEYS_REGRADE");
const bridgeUrl = env("MISTY_BROWSER_EVAL_BRIDGE").replace(/\/$/, "");
export const bridge = bridgeUrl
  ? { url: bridgeUrl, token: env("MISTY_BROWSER_EVAL_BRIDGE_TOKEN") }
  : undefined;
