// The Odysseys benchmark through Misty's product path (docs/plans/odysseys-campaign.md):
// each task is a composer request on the local stack, with this computer as the device,
// so the agent searches, fetches and opens its own browser tab as it would for a person.
// The recorder pictures the agent's tabs as it goes; Odysseys' per-rubric judge grades.
// Batches stop early when failures pile up, so fixes come before more spending.
//
//   MISTY_ODYSSEYS=1 npm run eval:odysseys          (see README.md for the setup)
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { expect, it } from "vitest";
import { EvalClient } from "../workflows/client.js";
import { device, deviceContexts } from "../workflows/device.js";
import { runUsage } from "../workflows/usage.js";
import { judgeTask } from "./judge.js";
import { Recorder, type Step } from "./recorder.js";
import { leakedProcesses, pageProcesses } from "./leak-check.js";
import { Ledger, searchesUsd, tokenUsd } from "./spend.js";
import { ProgressWatch } from "./watchdog.js";
import { loadTasks, selectTasks, type OdysseysTask } from "./tasks.js";

const env = (name: string, fallback = "") => process.env[name]?.trim() || fallback;
const list = (value: string) =>
  value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
const model = env("MISTY_ODYSSEYS_MODEL", "anthropic/claude-haiku-5.5");
const effort = env("MISTY_ODYSSEYS_EFFORT", "low");
// Model turns per task: 200, the most a dev eval may ask for, as the leaders run 100 to 200.
const turns = Number(env("MISTY_ODYSSEYS_TURNS", "200"));
const judgeModel = env("MISTY_ODYSSEYS_JUDGE", "anthropic/claude-haiku-5.5");
const split = env("MISTY_ODYSSEYS_SPLIT", "dev");
const limit = Number(env("MISTY_ODYSSEYS_LIMIT", "10"));
const offset = Number(env("MISTY_ODYSSEYS_OFFSET", "0"));
const earlyStop = env("MISTY_ODYSSEYS_EARLY_STOP", "1") === "1";
// No clock ends a run: Odysseys tasks are long on purpose. The progress watch (watchdog.ts)
// looks every few minutes and stops only a run that went quiet or only repeats itself.
// MISTY_ODYSSEYS_TASK_MINUTES still sets a hard cap when one is wanted; past it the pass stops.
const taskMs = env("MISTY_ODYSSEYS_TASK_MINUTES") ? Number(env("MISTY_ODYSSEYS_TASK_MINUTES")) * 60_000 : Infinity;
const watchMs = Number(env("MISTY_ODYSSEYS_WATCH_MINUTES", "3")) * 60_000;
const parallel = Number(env("MISTY_ODYSSEYS_PARALLEL", "3"));
const here = resolve(import.meta.dirname);
const ledger = new Ledger(
  join(here, "reports", "ledger.json"),
  Number(env("MISTY_ODYSSEYS_CAP", "50")),
);
// Checkpoints: MISTY_ODYSSEYS_RESUME names a task's folder from an earlier pass. That task goes
// on in the same conversation from where it stopped (after a fix, instead of starting over): the
// request and its last answer are the chat's history, the new turn asks for what remains, and the
// judge grades both runs' steps together as one trajectory.
const resumeDir = env("MISTY_ODYSSEYS_RESUME");
// MISTY_ODYSSEYS_REGRADE names a task's folder to judge again from its saved steps and pictures,
// without running it (after a judge fix, or a verdict the judge never gave).
const regradeDir = env("MISTY_ODYSSEYS_REGRADE");
const bridgeUrl = env("MISTY_BROWSER_EVAL_BRIDGE").replace(/\/$/, "");
const bridge = bridgeUrl
  ? { url: bridgeUrl, token: env("MISTY_BROWSER_EVAL_BRIDGE_TOKEN") }
  : undefined;

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
  /** The agent's own estimate of the task's minutes, from its checklist; 0 when it gave none. */
  estimateMinutes: number;
  costUsd: number;
  answer: string;
  dir: string;
  invocationId: string;
  conversationId: string;
  /** The earlier run this one continued, when it resumed from a checkpoint. */
  resumedFrom?: string;
}

/** Where an earlier run of a task stopped: what it answered and the conversation to continue. */
interface Checkpoint {
  dir: string;
  taskId: string;
  answer: string;
  invocationId: string;
  conversationId: string;
  seconds: number;
}

/** A run's conversation, from the local stack's database. */
function conversationOf(invocationId: string): string {
  if (!/^invocation_[0-9a-f-]{36}$/.test(invocationId)) return "";
  const container = env("MISTY_ACCEPTANCE_DB_CONTAINER", "misty-server-postgres-1");
  return execFileSync("docker", ["exec", container, "psql", "-U", "misty", "-d", "misty_server", "-t", "-A", "-c",
    `SELECT coalesce(conversation_id,'') FROM ai_invocations WHERE id='${invocationId}'`], { encoding: "utf8" }).trim();
}

function checkpointAt(dir: string): Checkpoint {
  const file = join(dir, "result.json");
  if (!existsSync(file)) throw new Error(`No result.json in ${dir}: only a graded run can be resumed.`);
  const run = JSON.parse(readFileSync(file, "utf8")) as Partial<OdysseysRun> & { taskId: string };
  const invocationId = run.invocationId ?? "";
  const conversationId = run.conversationId || conversationOf(invocationId);
  if (!conversationId) throw new Error(`${dir} has no conversation to continue (result.json needs its invocationId).`);
  return { dir, taskId: run.taskId, answer: run.answer ?? "", invocationId, conversationId, seconds: run.seconds ?? 0 };
}

/** The turn that resumes a task: what remains, then the whole answer again. */
const resumePrompt =
  "Continue the task above from where you stopped. Your last answer says what you found and what was not done: do what remains, " +
  "and make sure every page the request asks to keep open is open in its own tab. Then give the complete final answer for the whole " +
  "request, including what you found before.";

interface Performed {
  task: OdysseysTask;
  checkpoint?: Checkpoint;
  dir: string;
  recorder: Recorder;
  result: Awaited<ReturnType<EvalClient["run"]>>;
  seconds: number;
}

/** Runs one task through the product path and records it; grading comes later. */
async function perform(
  client: EvalClient,
  task: OdysseysTask,
  agentId: string,
  userId: string,
  dir: string,
  checkpoint?: Checkpoint,
): Promise<Performed> {
  const recorder = new Recorder(dir, bridge);
  if (checkpoint) recorder.carry(checkpoint.dir);
  await recorder.start();
  const watch = new ProgressWatch(task.id.slice(0, 10));
  const observe = (event: Record<string, unknown>) => {
    watch.observe(event);
    return recorder.observe(event);
  };
  const looks = { everyMs: watchMs, check: () => watch.check() };
  const started = performance.now();
  const body = {
    prompt: checkpoint ? resumePrompt : task.prompt,
    ...(checkpoint && {
      conversation_id: checkpoint.conversationId,
      history_turns: [{ invocation_id: checkpoint.invocationId, prompt: task.prompt, reply: checkpoint.answer, created_at: new Date().toISOString() }],
    }),
    model_override: model,
    reasoning_effort: effort,
    window_label: "main",
    agent_id: agentId,
    device_contexts: await deviceContexts(userId, agentId),
    mode: "drawer",
    surface_id: "global",
    eval_turn_limit: turns,
  };
  let result = await client.run(body, taskMs, [], observe, looks);
  // A run that failed before its first tool call with no answer never reached the agent (the
  // model provider was unreachable, say): run the task once more rather than grade nothing.
  if (result.terminal === "invocation.failed" && !result.tools.length && !result.answer.trim()) {
    process.stderr.write(`RETRY ${task.id.slice(0, 10)}: the run failed before any work\n`);
    result = await client.run(
      { ...body, device_contexts: await deviceContexts(userId, agentId) },
      taskMs,
      [],
      observe,
      looks,
    );
  }
  const seconds = (performance.now() - started) / 1000;
  await recorder.finish(result.answer);
  return { task, checkpoint, dir, recorder, result, seconds };
}

/** A run that made no call and wrote no answer (cancelled, or refused at the start) did no work. */
const didNoWork = (result: Performed["result"]) => !result.tools.length && !result.answer.trim();

/** Judges a recorded run and prices it; runs beside the next task's run. A run that did no work
 * is not sent to the judge: every rubric fails, and minutes of grading are saved. */
async function grade({ task, checkpoint, dir, recorder, result, seconds }: Performed): Promise<OdysseysRun> {
  const verdict = didNoWork(result)
    ? {
        rubrics: task.rubrics.map((rubric) => ({ id: rubric.id, success: false, reasoning: `Not graded: the run did no work (${result.terminal}).` })),
        average: 0, perfect: false, inputTokens: 0, outputTokens: 0,
      }
    : await judgeTask(judgeModel, task, recorder.steps, recorder.screenshots);
  const usage = await runUsage(result.invocationId).catch(() => undefined);
  const searches = result.tools.filter((tool) => tool === "web_search").length;
  const costUsd =
    (usage ? tokenUsd(model, usage.inputTokens, usage.cachedInputTokens, usage.outputTokens) : 0) +
    tokenUsd(judgeModel, verdict.inputTokens, 0, verdict.outputTokens) +
    searchesUsd(searches);
  ledger.add(costUsd);
  const run: OdysseysRun = {
    taskId: task.id,
    level: task.level,
    split: task.split,
    referenceLength: task.referenceLength,
    terminal: result.terminal,
    perfect: verdict.perfect,
    average: verdict.average,
    failed: verdict.rubrics
      .filter((item) => !item.success)
      .map(({ id, reasoning }) => ({ id, reasoning })),
    tools: result.tools,
    turns: usage?.turns ?? 0,
    // A resumed task's time is both runs': the time the task took.
    seconds: Number((seconds + (checkpoint?.seconds ?? 0)).toFixed(1)),
    estimateMinutes: estimateOf(result.toolResults),
    costUsd: Number(costUsd.toFixed(4)),
    answer: result.answer.slice(0, 4_000),
    dir,
    invocationId: result.invocationId,
    conversationId: conversationOf(result.invocationId),
    ...(checkpoint && { resumedFrom: checkpoint.dir }),
  };
  writeFileSync(
    join(dir, "result.json"),
    JSON.stringify({ ...run, prompt: task.prompt, rubrics: verdict.rubrics }, null, 2),
  );
  return run;
}

const failedRun = (task: OdysseysTask, dir: string, error: unknown): OdysseysRun => ({
  taskId: task.id,
  level: task.level,
  split: task.split,
  referenceLength: task.referenceLength,
  terminal: `error: ${String(error).slice(0, 200)}`,
  perfect: false,
  average: 0,
  failed: [],
  tools: [],
  turns: 0,
  seconds: 0,
  estimateMinutes: 0,
  costUsd: 0,
  answer: "",
  dir,
  invocationId: "",
  conversationId: "",
});

/** The agent's estimate from its checklist results (the tool echoes it back). */
function estimateOf(toolResults: string[]): number {
  for (const result of toolResults) {
    const match = /^log\.work\.checklist → .*"estimateMinutes":\s*(\d+)/s.exec(result);
    if (match) return Number(match[1]);
  }
  return 0;
}

/** "est 8m": the agent's own estimate, marked "over" when the run took more than half again. */
const estimate = (run: OdysseysRun) =>
  run.estimateMinutes
    ? ` est ${run.estimateMinutes}m${run.seconds > run.estimateMinutes * 60 * 1.5 ? " OVER" : ""}`
    : " est none";
const line = (run: OdysseysRun) =>
  `${run.perfect ? "PERFECT" : "FAIL"} ${run.level} ${run.taskId.slice(0, 10)} rubric ${run.average.toFixed(2)} ${run.seconds}s${estimate(run)} ${run.tools.length} tools $${run.costUsd.toFixed(3)} ${run.terminal}${run.failed.length ? ` — missed ${run.failed.map((item) => item.id).join(",")}` : ""}\n`;

/** Four failures among a batch's first six finished tasks stop the pass: fix before spending more. */
export function shouldStop(batch: OdysseysRun[]): boolean {
  // Runs that never reached the agent (the stack refused them) say nothing about it.
  const first = batch.filter((run) => !infrastructure(run)).slice(0, 6);
  return first.length === 6 && first.filter((run) => !run.perfect).length >= 4;
}

/** A run the stack refused before the agent worked on it. */
const infrastructure = (run: OdysseysRun) =>
  run.terminal.startsWith("error:") && run.tools.length === 0;

function summary(all: OdysseysRun[]) {
  const runs = all.filter((run) => !infrastructure(run));
  const median = (values: number[]) =>
    values.length ? [...values].sort((a, b) => a - b)[Math.floor(values.length / 2)]! : 0;
  return {
    runs: runs.length,
    perfect: runs.filter((run) => run.perfect).length,
    perfectRate: runs.length
      ? Number((runs.filter((run) => run.perfect).length / runs.length).toFixed(3))
      : 0,
    rubricAverage: runs.length
      ? Number((runs.reduce((sum, run) => sum + run.average, 0) / runs.length).toFixed(3))
      : 0,
    medianSeconds: median(runs.map((run) => run.seconds)),
    medianToolCalls: median(runs.map((run) => run.tools.length)),
    medianStepsPerReference: Number(
      median(runs.map((run) => run.tools.length / Math.max(1, run.referenceLength))).toFixed(2),
    ),
    costUsd: Number(runs.reduce((sum, run) => sum + run.costUsd, 0).toFixed(4)),
  };
}

it.skipIf(env("MISTY_ODYSSEYS") !== "1" || !regradeDir)("judges a saved run again", async () => {
  const previous = JSON.parse(readFileSync(join(regradeDir, "result.json"), "utf8")) as OdysseysRun & { rubrics: unknown };
  const task = selectTasks(loadTasks(), "all", [previous.taskId])[0]!;
  const steps = readFileSync(join(regradeDir, "steps.jsonl"), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line) as Step);
  const pictures = steps.flatMap((step) => step.screenshots.map((file) => join(regradeDir, file))).filter((file) => existsSync(file));
  const verdict = await judgeTask(judgeModel, task, steps, pictures);
  const run: OdysseysRun = {
    ...previous,
    perfect: verdict.perfect,
    average: verdict.average,
    failed: verdict.rubrics.filter((item) => !item.success).map(({ id, reasoning }) => ({ id, reasoning })),
  };
  writeFileSync(join(regradeDir, "result.json"), JSON.stringify({ ...run, prompt: task.prompt, rubrics: verdict.rubrics }, null, 2));
  process.stderr.write(line(run));
});

it.skipIf(env("MISTY_ODYSSEYS") !== "1" || Boolean(regradeDir))(
  "runs Odysseys tasks through the product path",
  async () => {
    if (!device || !bridge)
      throw new Error(
        "Odysseys needs this computer as the device: MISTY_WORKFLOW_EVAL_DEVICE and the eval bridge (README.md).",
      );
    const client = await EvalClient.open();
    // Stopping the pass (Ctrl-C, pkill) cancels the run in progress instead of leaving it spending.
    for (const signal of ["SIGINT", "SIGTERM"] as const) {
      process.once(signal, () => {
        const cancels = [...client.active].map((id) =>
          client.raw(`/ai/invocations/${id}/cancel`, { method: "POST" }).catch(() => undefined),
        );
        void Promise.all(cancels).finally(() => process.exit(130));
      });
    }
    const agentId =
      (await client.json<{ agents: Array<{ id: string }> }>("/misty/agents")).agents[0]?.id ?? "";
    const me = await client.json<{ id: string }>("/me");
    const checkpoint = resumeDir ? checkpointAt(resumeDir) : undefined;
    const tasks = checkpoint
      ? selectTasks(loadTasks(), "all", [checkpoint.taskId])
      : selectTasks(loadTasks(), split, list(env("MISTY_ODYSSEYS_TASKS"))).slice(offset, offset + limit);
    // The leak check (leak-check.ts): page processes before the pass, compared after it.
    const processesBefore = await pageProcesses(bridge);
    const stamp = new Date().toISOString().replace(/[:.]/g, "-");
    const root = join(here, "reports", stamp);
    const runs: OdysseysRun[] = [];
    const grading: Array<Promise<void>> = [];
    let stopped = "";
    let refusedInARow = 0;
    // Several tasks at once, each agent with its own tabs (MISTY_ODYSSEYS_PARALLEL, 3 by default):
    // a batch takes a third of the time, and agents working side by side is what the product does.
    let next = 0;
    const lane = async () => {
      while (!stopped && next < tasks.length) {
        const task = tasks[next++]!;
        if (!ledger.allows()) {
          stopped = `cap: $${ledger.spent.toFixed(2)} of $${ledger.cap.toFixed(2)} spent`;
          return;
        }
        // Stop on the judgments that have come back, without waiting for the rest.
        if (earlyStop && shouldStop(runs)) {
          stopped = "4 of the first 6 tasks failed: attribute and fix before running more";
          return;
        }
        const dir = join(root, task.id.slice(0, 10));
        mkdirSync(dir, { recursive: true });
        let performed: Performed;
        try {
          performed = await perform(client, task, agentId, me.id, dir, checkpoint);
          refusedInARow = 0;
          // The run's end, as it happens: grading takes minutes more, and the trajectory can be
          // read meanwhile (steps.jsonl is already written).
          process.stderr.write(
            `RUN ENDED ${task.id.slice(0, 10)} ${performed.result.terminal} ${performed.seconds.toFixed(0)}s ${performed.result.tools.length} tools — ${didNoWork(performed.result) ? "no work, not graded" : "grading"} ${dir}\n`,
          );
          // Out of AI usage, every later task would fail in a second: stop instead of grading them.
          if (/usage limit/i.test(performed.result.answer) && performed.result.tools.length < 3) {
            stopped =
              "the account is out of AI usage: raise the eval account's allowance, then run again";
            return;
          }
          if (performed.result.terminal === "timeout") {
            const counts = new Map<string, number>();
            for (const tool of performed.result.tools)
              counts.set(tool, (counts.get(tool) ?? 0) + 1);
            const busiest = [...counts]
              .sort((a, b) => b[1] - a[1])
              .slice(0, 6)
              .map(([tool, count]) => `${tool} ×${count}`)
              .join(", ");
            stopped = `${task.id.slice(0, 10)} ran past ${taskMs / 60_000} minutes (${busiest}): find what it looped on and fix it before running more`;
            grading.push(
              grade(performed)
                .catch((error) => failedRun(task, dir, error))
                .then((run) => {
                  runs.push(run);
                  process.stderr.write(line(run));
                }),
            );
            return;
          }
        } catch (error) {
          const run = failedRun(task, dir, error);
          runs.push(run);
          process.stderr.write(line(run));
          if (++refusedInARow >= 3) {
            stopped = `three runs in a row never reached the agent (${run.terminal}): check the stack's logs and disk`;
            return;
          }
          continue;
        }
        grading.push(
          grade(performed)
            .catch((error) => failedRun(task, dir, error))
            .then((run) => {
              runs.push(run);
              process.stderr.write(line(run));
            }),
        );
      }
    };
    await Promise.all(Array.from({ length: Math.max(1, parallel) }, () => lane()));
    await Promise.all(grading);
    if (!stopped && earlyStop && shouldStop(runs))
      stopped = "4 of the first 6 tasks failed: attribute and fix before running more";
    const leaked = processesBefore ? await leakedProcesses(bridge, processesBefore) : 0;
    if (leaked && !stopped) stopped = `the pass left ${leaked} WebKit page processes running: find the page that was not closed`;
    mkdirSync(root, { recursive: true });
    const report = {
      model,
      effort,
      judgeModel,
      split,
      offset,
      limit,
      stopped,
      ledgerUsd: Number(ledger.spent.toFixed(4)),
      pageProcesses: { before: processesBefore?.all.length, leaked },
      summary: summary(runs),
      runs,
    };
    writeFileSync(join(root, "report.json"), JSON.stringify(report, null, 2));
    process.stderr.write(
      `\n${JSON.stringify(report.summary)}\nCampaign spend $${ledger.spent.toFixed(2)} of $${ledger.cap.toFixed(2)}${stopped ? ` — stopped: ${stopped}` : ""}\nReport: ${join(root, "report.json")}\n`,
    );
    expect(runs.length + (stopped ? 1 : 0)).toBeGreaterThan(0);
  },
);
