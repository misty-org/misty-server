// The workflow eval (docs/plans/agent-harness.md, phase 0). Each task is one
// real request through the whole harness on a local stack: the server's own
// system prompt, catalog, routing and screen hand-offs. It scores the answer
// and the route, and stops before the AI Gateway spend passes the budget.
//
//   MISTY_ACCEPTANCE_API=http://127.0.0.1:8081 npm run eval:workflows
//
// See README.md for the stack, accounts, filters and the report.
import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { expect, it } from "vitest";
import { judge } from "../browser/judge.js";
import { serveSites } from "../browser/tasks.js";
import { Budget } from "./budget.js";
import { EvalClient } from "./client.js";
import { device, deviceContexts } from "./device.js";
import { composioFor } from "./composio.js";
import { StateReader } from "./state.js";
import { expectPassed, passed, report, routing, table, type WorkflowRun } from "./score.js";
import { displayCapture, loadTasks, skipReason, type WorkflowTask } from "./tasks.js";
import { runUsage } from "./usage.js";

const here = resolve(import.meta.dirname);
const list = (value: string | undefined) => (value ?? "").split(",").map((item) => item.trim()).filter(Boolean);
// The cheapest current model of each provider's line.
const models = list(process.env.MISTY_WORKFLOW_EVAL_MODELS || "anthropic/claude-haiku-5.5,openai/gpt-6-luna,google/gemini-3.5-flash-lite");
// The run's reasoning effort; the server honors it only with MISTY_ALLOW_MODEL_OVERRIDE.
const effort = process.env.MISTY_WORKFLOW_EVAL_EFFORT?.trim() || "low";
const judgeModel = process.env.MISTY_WORKFLOW_EVAL_JUDGE?.trim() || "anthropic/claude-haiku-5.5";
// One run can't tell an improvement from luck, so each task runs this many times.
const repeat = Math.max(1, Math.min(10, Number(process.env.MISTY_WORKFLOW_EVAL_REPEAT || 3)));
const only = new Set(list(process.env.MISTY_WORKFLOW_EVAL_TASKS));
const accounts = new Set(list(process.env.MISTY_WORKFLOW_EVAL_ACCOUNTS));
const enabled = process.env.MISTY_WORKFLOW_EVAL === "1";

const selected = (task: WorkflowTask) => !only.size || only.has(task.id) || only.has(task.category) || only.has(task.source);

/** The request each agent.entry sends today. Schedules run as the composer until the eval can claim one. */
function request(task: WorkflowTask, model: string, agentId: string, siteBase: string, contexts: unknown[]): Record<string, unknown> {
  const base = {
    prompt: task.prompt, model_override: model, reasoning_effort: effort, window_label: "main",
    ...(agentId ? { agent_id: agentId } : {}), ...(contexts.length ? { device_contexts: contexts } : {}),
  };
  if (task.surface === "cursor") {
    return { ...base, mode: "companion", surface_id: "global", companion_mode: "auto", ...(task.start?.screen ? { display_captures: [displayCapture(task.start.screen)] } : {}) };
  }
  const tab = task.start?.tab ? { current_tab: { title: task.start.tab.site, url: `${siteBase}/${task.start.tab.site}` } } : {};
  return { ...base, mode: "drawer", surface_id: "global", ...tab };
}

async function runTask(client: EvalClient, budget: Budget, task: WorkflowTask, model: string, agentId: string, siteBase: string, sign: () => Promise<unknown[]>, state: StateReader | undefined): Promise<WorkflowRun> {
  const before = budget.balance;
  const started = performance.now();
  const startedAt = new Date();
  // A fresh device grant per run, and one more try with a new one if the server refuses it.
  let result;
  try {
    result = await client.run(request(task, model, agentId, siteBase, await sign()), undefined, task.approve);
  } catch (error) {
    if (!String(error).includes("device_grant_required")) throw error;
    result = await client.run(request(task, model, agentId, siteBase, await sign()), undefined, task.approve);
  }
  const run: WorkflowRun = {
    taskId: task.id, category: task.category, source: task.source, surface: task.surface, model,
    terminal: result.terminal, tools: result.tools, screenRequests: result.screenRequests,
    answer: result.answer.slice(0, 4_000), ms: performance.now() - started, costUsd: 0,
    routing: routing(task, result.tools, result.screenRequests),
    usage: await runUsage(result.invocationId).catch(() => undefined),
  };
  // A run that ended incomplete but answered is graded on its answer; only a
  // run with nothing to show is an error.
  if (!/completed$/.test(result.terminal) && !result.answer.trim()) run.error = `run ended: ${result.terminal}`;
  // A run stopped at a screen hand-off has no answer to grade yet; its route still counts.
  const handedOff = result.screenRequests.length > 0 && !device;
  const textPassed = expectPassed(result.answer, task.expect);
  // What the task changed in the person's apps, read back so the judge needn't trust the answer.
  const endState = task.state?.length && state ? await state.read(task.state, startedAt) : "";
  if (!handedOff && task.rubric?.length) {
    try {
      const verdict = await judge({
        model: judgeModel, prompt: task.prompt, rubric: task.rubric, truth: task.truth,
        subject: "a personal assistant agent. Its web_search and web_fetch tools read the web in the background: they never open a browser or touch the person's screen. Only screen_open, screen_look, browser_* and desktop tools do",
        // Raw tool results, when the server sends them, are fuller than the rows' titles.
        answer: result.answer, toolCalls: result.tools, pagesRead: result.toolResults.length ? evenShares(result.toolResults) : result.pagesRead,
        pageMarkdown: endState ? `End state read back from the person's apps after the run (this is the evidence for what was created, changed or sent):\n${endState}` : "",
        // The judge sees the screen the agent saw, so claims about it can be checked.
        ...(task.start?.screen ? { screenshot: { data: displayCapture(task.start.screen).data_url.split(",")[1] ?? "", mediaType: "image/png" } } : {}),
      });
      run.answerPassed = verdict.passed && textPassed !== false;
      run.judgeReasons = verdict.criteria.map((item) => `${item.met ? "✓" : "✗"} ${item.criterion}: ${item.reason}`);
    } catch (error) {
      run.error = `judge: ${String(error).slice(0, 200)}`;
    }
  } else if (!handedOff && textPassed !== undefined) {
    run.answerPassed = textPassed;
  }
  // Remove what the run created, so the next run starts from the seeded state.
  if (task.state?.length && state) {
    const failures = await state.undo(task.state, startedAt);
    if (failures.length) run.undoFailures = failures;
  }
  run.costUsd = Number((await budget.since(before)).toFixed(4));
  return run;
}

/** Each result's share of the judge's 60,000-character budget, so early reads aren't the ones dropped. */
function evenShares(results: string[]): string[] {
  const share = Math.max(1_500, Math.floor(58_000 / Math.max(results.length, 1)));
  return results.map((item) => (item.length > share ? `${item.slice(0, share)}…` : item));
}

it.skipIf(!enabled)("runs workflow tasks within budget", async () => {
  const budget = await Budget.open();
  const client = await EvalClient.open();
  const agents = await client.json<{ agents: Array<{ id: string }> }>("/misty/agents");
  const agentId = agents.agents[0]?.id ?? "";
  const me = await client.json<{ id: string }>("/me");
  const sign = () => deviceContexts(me.id, agentId);
  // Reads end state for account tasks; without connected apps those tasks are skipped anyway.
  const state = accounts.size ? new StateReader(client, await composioFor(client)) : undefined;
  const tasks = loadTasks().filter(selected);
  const sites = await serveSites();
  const runs: WorkflowRun[] = [];
  const skipped: Record<string, string> = {};
  let stopped = "";
  // Runs refused before the model ever ran (an account limit or a broken stack) say nothing about the agent.
  let refusedInARow = 0;
  try {
    outer: for (const model of models) {
      for (const task of tasks) for (let attempt = 1; attempt <= repeat; attempt++) {
        const reason = skipReason(task, accounts, device);
        if (reason) {
          skipped[task.id] = reason;
          break;
        }
        let allowed: boolean;
        try {
          allowed = await budget.allows();
        } catch (error) {
          stopped = `the AI Gateway balance could not be read: ${String(error).slice(0, 120)}`;
          break outer;
        }
        if (!allowed) {
          stopped = `budget: $${budget.spent.toFixed(2)} of $${budget.cap.toFixed(2)} spent`;
          break outer;
        }
        let run: WorkflowRun;
        try {
          run = await runTask(client, budget, task, model, agentId, sites.base, sign, state);
        } catch (error) {
          // One broken run never ends the pass; it is recorded as that task's error.
          run = {
            taskId: task.id, category: task.category, source: task.source, surface: task.surface, model, terminal: "error",
            tools: [], screenRequests: [], answer: "", ms: 0, costUsd: 0, error: String(error).slice(0, 300),
            routing: { passed: false, broken: ["the run could not start"], unavailable: [], runs: 0 },
          };
        }
        run.attempt = attempt;
        runs.push(run);
        refusedInARow = run.error && run.tools.length === 0 && run.ms < 3_000 ? refusedInARow + 1 : 0;
        if (refusedInARow >= 3) {
          stopped = "three runs in a row failed before the model ran: check the eval account's plan and the stack's logs";
          break outer;
        }
        const verdict = passed(run) ? "PASS" : run.routing.passed ? "FAIL answer" : "FAIL route";
        process.stderr.write(`${verdict} ${model} ${task.id}#${attempt} ${(run.ms / 1000).toFixed(1)}s ${run.tools.length} tools ${run.routing.runs} runs $${run.costUsd.toFixed(3)}${run.routing.broken.length ? ` — ${run.routing.broken.join("; ")}` : ""}${run.error ? ` — ${run.error}` : ""}\n`);
      }
    }
  } finally {
    await sites.close();
  }
  const dir = join(here, "reports");
  mkdirSync(dir, { recursive: true });
  const file = join(dir, `report-${new Date().toISOString().replace(/[:.]/g, "-")}.json`);
  writeFileSync(file, JSON.stringify({ models, effort, judgeModel, repeat, spentUsd: Number(budget.spent.toFixed(4)), balanceUsd: budget.balance, stopped, summary: report(runs), skipped, runs }, null, 2));
  if (runs.length) process.stderr.write(`\n${table(runs)}\n`);
  process.stderr.write(`\nSpent $${budget.spent.toFixed(2)} (balance $${budget.balance.toFixed(2)})${stopped ? ` — stopped, ${stopped}` : ""}\nReport: ${file}\n`);
  expect(runs.length + Object.keys(skipped).length).toBeGreaterThan(0);
});
