// The Odysseys benchmark through Misty's product path (docs/agents/evals/odysseys.md):
// each task is a composer request on the local stack, with this computer as the device,
// so the agent searches, fetches and opens its own browser tab as it would for a person.
// The recorder pictures the agent's tabs as it goes; Odysseys' per-rubric judge grades.
// Batches stop early when failures pile up, so fixes come before more spending.
//
//   MISTY_ODYSSEYS=1 npm run eval:odysseys          (see README.md for the setup)
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";
import { checkpointAt, type Checkpoint } from "./checkpoints.js";
import { EvalClient } from "./client.js";
import { bridge, earlyStop, env, here, judgeModel, ledger, limit, list, model, effort, offset, parallel, regradeDir, resumeDir, resumeFailedDir, split, taskMs } from "./config.js";
import { device } from "./device.js";
import { judgeTask } from "./judge.js";
import { leakedProcesses, pageProcesses } from "./leak-check.js";
import { didNoWork, grade, perform, type Performed } from "./perform.js";
import type { Step } from "./recorder.js";
import { failedRun, infrastructure, line, shouldStop, summary, type OdysseysRun } from "./results.js";
import { loadTasks, selectTasks } from "./tasks.js";

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
    const checkpoints = new Map<string, Checkpoint>();
    const carried: OdysseysRun[] = [];
    if (resumeDir) {
      const one = checkpointAt(resumeDir);
      checkpoints.set(one.taskId, one);
    }
    if (resumeFailedDir) {
      const earlier = JSON.parse(readFileSync(join(resumeFailedDir, "report.json"), "utf8")) as { runs: OdysseysRun[] };
      for (const run of earlier.runs) {
        if (run.perfect) carried.push(run);
        else if (!infrastructure(run) && run.dir && existsSync(join(run.dir, "result.json"))) {
          const one = checkpointAt(run.dir);
          checkpoints.set(one.taskId, one);
        }
      }
    }
    const resumed = checkpoints.size > 0;
    const tasks = resumed
      ? selectTasks(loadTasks(), "all", [...checkpoints.keys()])
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
        if (earlyStop && !resumed && shouldStop(runs)) {
          stopped = "4 of the first 6 tasks failed: attribute and fix before running more";
          return;
        }
        const dir = join(root, task.id.slice(0, 10));
        mkdirSync(dir, { recursive: true });
        let performed: Performed;
        try {
          performed = await perform(client, task, agentId, me.id, dir, checkpoints.get(task.id));
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
    if (!stopped && earlyStop && !resumed && shouldStop(runs))
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
      resumed,
      carried: carried.length,
      summary: summary([...carried, ...runs]),
      runs,
    };
    writeFileSync(join(root, "report.json"), JSON.stringify(report, null, 2));
    process.stderr.write(
      `\n${JSON.stringify(report.summary)}\nCampaign spend $${ledger.spent.toFixed(2)} of $${ledger.cap.toFixed(2)}${stopped ? ` — stopped: ${stopped}` : ""}\nReport: ${join(root, "report.json")}\n`,
    );
    // The gate: when every task that was resumed now passes, the pass is not the score. A full
    // pass is, so say so and record it.
    if (resumed && !stopped && runs.length && runs.every((run) => run.perfect)) {
      writeFileSync(join(here, "reports", "gate.json"), JSON.stringify({ passedAt: new Date().toISOString(), report: join(root, "report.json"), resumedFrom: resumeFailedDir || resumeDir }, null, 2));
      process.stderr.write("GATE: every resumed task passed. Run the full batch once, without RESUME, for the score that counts.\n");
    }
    expect(runs.length + (stopped ? 1 : 0)).toBeGreaterThan(0);
  },
);
