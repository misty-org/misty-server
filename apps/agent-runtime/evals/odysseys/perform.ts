// Running one task through the product path, then judging and pricing it.
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { ledgerFromPrompt, resumeText, stripLedger } from "../../src/ledger.js";
import { resumePrompt, conversationOf, type Checkpoint } from "./checkpoints.js";
import type { EvalClient } from "./client.js";
import { bridge, effort, judgeModel, ledger, model, promptOff, taskMs, turns, watchMs } from "./config.js";
import { deviceContexts } from "./device.js";
import { judgeTask } from "./judge.js";
import { Recorder } from "./recorder.js";
import type { OdysseysRun } from "./results.js";
import { searchesUsd, tokenUsd } from "./spend.js";
import type { OdysseysTask } from "./tasks.js";
import { runUsage } from "./usage.js";
import { ProgressWatch } from "./watchdog.js";

export interface Performed {
  task: OdysseysTask;
  checkpoint?: Checkpoint;
  dir: string;
  recorder: Recorder;
  result: Awaited<ReturnType<EvalClient["run"]>>;
  seconds: number;
}

/** Runs one task through the product path and records it; grading comes later. */
export async function perform(
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
    prompt: checkpoint ? (checkpoint.ledger.length ? resumeText(checkpoint.ledger) : resumePrompt) : task.prompt,
    ...(checkpoint && {
      conversation_id: checkpoint.conversationId,
      history_turns: [{ invocation_id: checkpoint.invocationId, prompt: task.prompt, reply: stripLedger(checkpoint.answer), created_at: new Date().toISOString() }],
    }),
    model_override: model,
    reasoning_effort: effort,
    window_label: "main",
    agent_id: agentId,
    device_contexts: await deviceContexts(userId, agentId),
    mode: "drawer",
    surface_id: "global",
    eval_turn_limit: turns,
    ...(promptOff.length && { prompt_off: promptOff }),
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
export const didNoWork = (result: Performed["result"]) => !result.tools.length && !result.answer.trim();

/** Judges a recorded run and prices it; runs beside the next task's run. A run that did no work
 * is not sent to the judge: every rubric fails, and minutes of grading are saved. */
export async function grade({ task, checkpoint, dir, recorder, result, seconds }: Performed): Promise<OdysseysRun> {
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
    answer: stripLedger(result.answer).slice(0, 4_000),
    ...(ledgerFromPrompt(result.answer).length && { ledger: ledgerFromPrompt(result.answer) }),
    dir,
    invocationId: result.invocationId,
    conversationId: conversationOf(result.invocationId),
    ...(checkpoint && { resumedFrom: checkpoint.dir }),
    ...(promptOff.length && { promptOff }),
  };
  writeFileSync(
    join(dir, "result.json"),
    JSON.stringify({ ...run, prompt: task.prompt, rubrics: verdict.rubrics }, null, 2),
  );
  return run;
}

/** The agent's estimate from its checklist results (the tool echoes it back). */
function estimateOf(toolResults: string[]): number {
  for (const result of toolResults) {
    const match = /^log\.work\.checklist → .*"estimateMinutes":\s*(\d+)/s.exec(result);
    if (match) return Number(match[1]);
  }
  return 0;
}
