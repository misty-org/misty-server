// The Odysseys benchmark through Misty's product path (docs/plans/odysseys-campaign.md):
// each task is a composer request on the local stack, with this computer as the device,
// so the agent searches, fetches and opens its own browser tab as it would for a person.
// The recorder pictures the agent's tabs as it goes; Odysseys' per-rubric judge grades.
// Batches stop early when failures pile up, so fixes come before more spending.
//
//   MISTY_ODYSSEYS=1 npm run eval:odysseys          (see README.md for the setup)
import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { expect, it } from "vitest";
import { EvalClient } from "../workflows/client.js";
import { device, deviceContexts } from "../workflows/device.js";
import { runUsage } from "../workflows/usage.js";
import { judgeTask } from "./judge.js";
import { Recorder } from "./recorder.js";
import { Ledger, searchesUsd, tokenUsd } from "./spend.js";
import { loadTasks, selectTasks, type OdysseysTask } from "./tasks.js";

const env = (name: string, fallback = "") => process.env[name]?.trim() || fallback;
const list = (value: string) => value.split(",").map((item) => item.trim()).filter(Boolean);
const model = env("MISTY_ODYSSEYS_MODEL", "anthropic/claude-haiku-5.5");
const effort = env("MISTY_ODYSSEYS_EFFORT", "low");
const judgeModel = env("MISTY_ODYSSEYS_JUDGE", "anthropic/claude-haiku-5.5");
const split = env("MISTY_ODYSSEYS_SPLIT", "dev");
const limit = Number(env("MISTY_ODYSSEYS_LIMIT", "10"));
const offset = Number(env("MISTY_ODYSSEYS_OFFSET", "0"));
const earlyStop = env("MISTY_ODYSSEYS_EARLY_STOP", "1") === "1";
const here = resolve(import.meta.dirname);
const ledger = new Ledger(join(here, "reports", "ledger.json"), Number(env("MISTY_ODYSSEYS_CAP", "50")));
const bridgeUrl = env("MISTY_BROWSER_EVAL_BRIDGE").replace(/\/$/, "");
const bridge = bridgeUrl ? { url: bridgeUrl, token: env("MISTY_BROWSER_EVAL_BRIDGE_TOKEN") } : undefined;

export interface OdysseysRun {
  taskId: string;
  level: string;
  split: string;
  referenceLength: number;
  terminal: string;
  perfect: boolean;
  average: number;
  failed: Array<{ id: string; reasoning: string }>;
  tools: string[];
  turns: number;
  seconds: number;
  costUsd: number;
  answer: string;
  dir: string;
}

async function runTask(client: EvalClient, task: OdysseysTask, agentId: string, userId: string, dir: string): Promise<OdysseysRun> {
  const recorder = new Recorder(dir, bridge);
  await recorder.start();
  const started = performance.now();
  const body = {
    prompt: task.prompt, model_override: model, reasoning_effort: effort, window_label: "main", agent_id: agentId,
    device_contexts: await deviceContexts(userId, agentId), mode: "drawer", surface_id: "global",
  };
  const result = await client.run(body, 20 * 60_000, [], (event) => recorder.observe(event));
  const seconds = (performance.now() - started) / 1000;
  await recorder.finish(result.answer);
  const verdict = await judgeTask(judgeModel, task, recorder.steps, recorder.screenshots);
  const usage = await runUsage(result.invocationId).catch(() => undefined);
  const searches = result.tools.filter((tool) => tool === "web_search").length;
  const costUsd = (usage ? tokenUsd(model, usage.inputTokens, usage.cachedInputTokens, usage.outputTokens) : 0) +
    tokenUsd(judgeModel, verdict.inputTokens, 0, verdict.outputTokens) + searchesUsd(searches);
  ledger.add(costUsd);
  const run: OdysseysRun = {
    taskId: task.id, level: task.level, split: task.split, referenceLength: task.referenceLength, terminal: result.terminal,
    perfect: verdict.perfect, average: verdict.average, failed: verdict.rubrics.filter((item) => !item.success).map(({ id, reasoning }) => ({ id, reasoning })),
    tools: result.tools, turns: usage?.turns ?? 0, seconds: Number(seconds.toFixed(1)), costUsd: Number(costUsd.toFixed(4)), answer: result.answer.slice(0, 4_000), dir,
  };
  writeFileSync(join(dir, "result.json"), JSON.stringify({ ...run, prompt: task.prompt, rubrics: verdict.rubrics }, null, 2));
  return run;
}

/** Four failures among a batch's first six finished tasks stop the pass: fix before spending more. */
export function shouldStop(batch: OdysseysRun[]): boolean {
  // Runs that never reached the agent (the stack refused them) say nothing about it.
  const first = batch.filter((run) => !infrastructure(run)).slice(0, 6);
  return first.length === 6 && first.filter((run) => !run.perfect).length >= 4;
}

/** A run the stack refused before the agent worked on it. */
const infrastructure = (run: OdysseysRun) => run.terminal.startsWith("error:") && run.tools.length === 0;

function summary(all: OdysseysRun[]) {
  const runs = all.filter((run) => !infrastructure(run));
  const median = (values: number[]) => values.length ? [...values].sort((a, b) => a - b)[Math.floor(values.length / 2)]! : 0;
  return {
    runs: runs.length,
    perfect: runs.filter((run) => run.perfect).length,
    perfectRate: runs.length ? Number((runs.filter((run) => run.perfect).length / runs.length).toFixed(3)) : 0,
    rubricAverage: runs.length ? Number((runs.reduce((sum, run) => sum + run.average, 0) / runs.length).toFixed(3)) : 0,
    medianSeconds: median(runs.map((run) => run.seconds)),
    medianToolCalls: median(runs.map((run) => run.tools.length)),
    medianStepsPerReference: Number(median(runs.map((run) => run.tools.length / Math.max(1, run.referenceLength))).toFixed(2)),
    costUsd: Number(runs.reduce((sum, run) => sum + run.costUsd, 0).toFixed(4)),
  };
}

it.skipIf(env("MISTY_ODYSSEYS") !== "1")("runs Odysseys tasks through the product path", async () => {
  if (!device || !bridge) throw new Error("Odysseys needs this computer as the device: MISTY_WORKFLOW_EVAL_DEVICE and the eval bridge (README.md).");
  const client = await EvalClient.open();
  const agentId = (await client.json<{ agents: Array<{ id: string }> }>("/misty/agents")).agents[0]?.id ?? "";
  const me = await client.json<{ id: string }>("/me");
  const tasks = selectTasks(loadTasks(), split, list(env("MISTY_ODYSSEYS_TASKS"))).slice(offset, offset + limit);
  const stamp = new Date().toISOString().replace(/[:.]/g, "-");
  const root = join(here, "reports", stamp);
  const runs: OdysseysRun[] = [];
  let stopped = "";
  for (const task of tasks) {
    if (!ledger.allows()) {
      stopped = `cap: $${ledger.spent.toFixed(2)} of $${ledger.cap.toFixed(2)} spent`;
      break;
    }
    const dir = join(root, task.id.slice(0, 10));
    mkdirSync(dir, { recursive: true });
    let run: OdysseysRun;
    try {
      run = await runTask(client, task, agentId, me.id, dir);
    } catch (error) {
      run = { taskId: task.id, level: task.level, split: task.split, referenceLength: task.referenceLength, terminal: `error: ${String(error).slice(0, 200)}`,
        perfect: false, average: 0, failed: [], tools: [], turns: 0, seconds: 0, costUsd: 0, answer: "", dir };
    }
    runs.push(run);
    process.stderr.write(`${run.perfect ? "PERFECT" : "FAIL"} ${task.level} ${task.id.slice(0, 10)} rubric ${run.average.toFixed(2)} ${run.seconds}s ${run.tools.length} tools $${run.costUsd.toFixed(3)} ${run.terminal}${run.failed.length ? ` — missed ${run.failed.map((item) => item.id).join(",")}` : ""}\n`);
    if (runs.slice(-3).length === 3 && runs.slice(-3).every(infrastructure)) {
      stopped = `three runs in a row never reached the agent (${run.terminal}): check the stack's logs and disk`;
      break;
    }
    if (earlyStop && shouldStop(runs)) {
      stopped = "4 of the first 6 tasks failed: attribute and fix before running more";
      break;
    }
  }
  mkdirSync(root, { recursive: true });
  const report = { model, effort, judgeModel, split, offset, limit, stopped, ledgerUsd: Number(ledger.spent.toFixed(4)), summary: summary(runs), runs };
  writeFileSync(join(root, "report.json"), JSON.stringify(report, null, 2));
  process.stderr.write(`\n${JSON.stringify(report.summary)}\nCampaign spend $${ledger.spent.toFixed(2)} of $${ledger.cap.toFixed(2)}${stopped ? ` — stopped: ${stopped}` : ""}\nReport: ${join(root, "report.json")}\n`);
  expect(runs.length + (stopped ? 1 : 0)).toBeGreaterThan(0);
});
