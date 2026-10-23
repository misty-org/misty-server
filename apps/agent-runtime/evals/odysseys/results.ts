// One task's result, the pass's stop rule and its summary.
import type { LedgerRow } from "../../src/ledger.js";
import type { OdysseysTask } from "./tasks.js";

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
  /** The parts the agent kept track of and where each ended (src/ledger.ts). */
  ledger?: LedgerRow[];
  /** The earlier run this one continued, when it resumed from a checkpoint. */
  resumedFrom?: string;
}

export const failedRun = (task: OdysseysTask, dir: string, error: unknown): OdysseysRun => ({
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

/** "est 8m": the agent's own estimate, marked "over" when the run took more than half again. */
const estimate = (run: OdysseysRun) =>
  run.estimateMinutes
    ? ` est ${run.estimateMinutes}m${run.seconds > run.estimateMinutes * 60 * 1.5 ? " OVER" : ""}`
    : " est none";
export const line = (run: OdysseysRun) =>
  `${run.perfect ? "PERFECT" : "FAIL"} ${run.level} ${run.taskId.slice(0, 10)} rubric ${run.average.toFixed(2)} ${run.seconds}s${estimate(run)} ${run.tools.length} tools $${run.costUsd.toFixed(3)} ${run.terminal}${run.failed.length ? ` — missed ${run.failed.map((item) => item.id).join(",")}` : ""}\n`;

/** Four failures among a batch's first six finished tasks stop the pass: fix before spending more. */
export function shouldStop(batch: OdysseysRun[]): boolean {
  // Runs that never reached the agent (the stack refused them) say nothing about it.
  const first = batch.filter((run) => !infrastructure(run)).slice(0, 6);
  return first.length === 6 && first.filter((run) => !run.perfect).length >= 4;
}

/** A run the stack refused before the agent worked on it. */
export const infrastructure = (run: OdysseysRun) =>
  run.terminal.startsWith("error:") && run.tools.length === 0;

export function summary(all: OdysseysRun[]) {
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
