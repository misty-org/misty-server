// Scores workflow runs. Answer quality and routing are kept apart, so a right
// answer reached the long way is a routing failure, not a pass.
import { matches, todayName, type WorkflowTask } from "./tasks.js";
import type { RunUsage } from "./usage.js";

export interface WorkflowRun {
  taskId: string;
  category: string;
  source: string;
  surface: string;
  model: string;
  terminal: string;
  /** Tool calls in order, as the model named them. */
  tools: string[];
  /** Screens the run asked for; each ends the run and needs another today. */
  screenRequests: string[];
  answer: string;
  ms: number;
  costUsd: number;
  answerPassed?: boolean;
  judgeReasons?: string[];
  routing: RoutingResult;
  error?: string;
  /** Items the run created that the runner could not remove afterwards. */
  undoFailures?: string[];
  /** Which repetition of the task this was, from 1. */
  attempt?: number;
  /** Model turns and tokens from the billing ledger. */
  usage?: RunUsage;
}

export interface RoutingResult {
  passed: boolean;
  /** Each broken rule, in words. */
  broken: string[];
  /** Required tools that don't exist yet, so the rule can't be judged. */
  unavailable: string[];
  runs: number;
}

/** Searches and page reads, the calls the research budget (src/research-budget.ts) governs. */
export const researchCalls = (tools: string[]) => tools.filter((name) => name === "web_search" || name === "web_fetch").length;

export function routing(task: WorkflowTask, tools: string[], screenRequests: string[]): RoutingResult {
  const rules = task.tools ?? {};
  const broken: string[] = [];
  const unavailable: string[] = [];
  for (const required of rules.required ?? []) {
    const name = todayName(required);
    if (name === null) unavailable.push(required);
    else if (!tools.some((called) => matches(name, called))) broken.push(`never called ${required}`);
  }
  for (const forbidden of rules.forbidden ?? []) {
    const name = todayName(forbidden);
    if (name === null) continue;
    const hit = tools.find((called) => matches(name, called));
    if (hit) broken.push(`called ${hit}`);
  }
  if (rules.maxCalls !== undefined && tools.length > rules.maxCalls) broken.push(`${tools.length} tool calls, at most ${rules.maxCalls}`);
  if (rules.maxResearchCalls !== undefined && researchCalls(tools) > rules.maxResearchCalls)
    broken.push(`${researchCalls(tools)} searches and page reads, at most ${rules.maxResearchCalls}`);
  const runs = 1 + screenRequests.length;
  if (rules.maxRuns !== undefined && runs > rules.maxRuns) broken.push(`${runs} runs (screen hand-offs), at most ${rules.maxRuns}`);
  return { passed: broken.length === 0, broken, unavailable, runs };
}

export function expectPassed(answer: string, expected: string[] | undefined): boolean | undefined {
  if (!expected?.length) return undefined;
  const text = answer.toLowerCase();
  return expected.every((item) => text.includes(item.toLowerCase()));
}

export function passed(run: WorkflowRun): boolean {
  return !run.error && run.routing.passed && run.answerPassed !== false;
}

interface Spread {
  min: number;
  median: number;
  max: number;
}

interface Summary {
  runs: number;
  passed: number;
  /** Passed runs over all runs, 0 to 1. */
  passRate: number;
  routingPassed: number;
  answerPassed: number;
  answerGraded: number;
  toolCalls: Spread;
  /** Searches and page reads per run. */
  researchCalls: Spread;
  seconds: Spread;
  costUsd: number;
  handoffs: number;
  /** Input tokens billed at the cached price, over all input tokens. */
  cachedShare: number;
  cacheSavedUsd: number;
}

const median = (values: number[]) => [...values].sort((a, b) => a - b)[Math.floor(values.length / 2)] ?? 0;
const spread = (values: number[]): Spread =>
  values.length ? { min: Math.min(...values), median: median(values), max: Math.max(...values) } : { min: 0, median: 0, max: 0 };
const round = (value: number, places = 4) => Number(value.toFixed(places));

function summarize(runs: WorkflowRun[]): Summary {
  const graded = runs.filter((run) => run.answerPassed !== undefined);
  const input = runs.reduce((sum, run) => sum + (run.usage?.inputTokens ?? 0), 0);
  const cached = runs.reduce((sum, run) => sum + (run.usage?.cachedInputTokens ?? 0), 0);
  const ok = runs.filter(passed).length;
  return {
    runs: runs.length,
    passed: ok,
    passRate: runs.length ? round(ok / runs.length, 3) : 0,
    routingPassed: runs.filter((run) => run.routing.passed).length,
    answerPassed: graded.filter((run) => run.answerPassed).length,
    answerGraded: graded.length,
    toolCalls: spread(runs.map((run) => run.tools.length)),
    researchCalls: spread(runs.map((run) => researchCalls(run.tools))),
    seconds: spread(runs.map((run) => round(run.ms / 1000, 1))),
    costUsd: round(runs.reduce((sum, run) => sum + run.costUsd, 0)),
    handoffs: runs.reduce((sum, run) => sum + run.screenRequests.length, 0),
    cachedShare: input ? round(cached / input, 3) : 0,
    cacheSavedUsd: round(runs.reduce((sum, run) => sum + (run.usage?.cacheSavedUsd ?? 0), 0)),
  };
}

/** Summaries keyed "model", "model · category" and "model · task". */
export function report(runs: WorkflowRun[]): Record<string, Summary> {
  const groups = new Map<string, WorkflowRun[]>();
  for (const run of runs) {
    for (const key of [run.model, `${run.model} · ${run.category}`, `${run.model} · ${run.taskId}`]) groups.set(key, [...(groups.get(key) ?? []), run]);
  }
  return Object.fromEntries([...groups].map(([key, group]) => [key, summarize(group)]));
}

/** One line per model and task: pass rate and spread, for the end of a pass. */
export function table(runs: WorkflowRun[]): string {
  const summaries = report(runs);
  const models = [...new Set(runs.map((run) => run.model))];
  const lines: string[] = [];
  for (const model of models) {
    const all = summaries[model]!;
    lines.push(`${model}: ${all.passed}/${all.runs} passed (${Math.round(all.passRate * 100)}%), median ${all.toolCalls.median} calls (${all.researchCalls.median} research) and ${all.seconds.median}s, $${all.costUsd.toFixed(3)}, ${Math.round(all.cachedShare * 100)}% of input cached (saved $${all.cacheSavedUsd.toFixed(3)})`);
    for (const task of [...new Set(runs.filter((run) => run.model === model).map((run) => run.taskId))]) {
      const one = summaries[`${model} · ${task}`]!;
      lines.push(`  ${task}: ${one.passed}/${one.runs}, calls ${one.toolCalls.min}–${one.toolCalls.max} (research ${one.researchCalls.min}–${one.researchCalls.max}), ${one.seconds.min}–${one.seconds.max}s`);
    }
  }
  return lines.join("\n");
}
