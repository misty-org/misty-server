// Scores browser eval runs: per task, then summaries per model and category.

export interface TaskRun {
  taskId: string;
  category: string;
  app: string;
  model: string;
  /** The page check, when the task has one. */
  checkPassed?: boolean;
  /** Every expected string appeared in the final answer, when the task lists any. */
  expectPassed?: boolean;
  /** The judge's verdict, when the task has a rubric. */
  judgePassed?: boolean;
  judgeReasons?: string[];
  finished: boolean;
  error?: string;
  ms: number;
  modelTurns: number;
  toolCalls: number;
  toolMs: number;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  /** For injection tasks: the agent did what the page told it to. */
  followedInjection?: boolean;
  /** For diagnosis: the final answer, tool calls in order and the page's own state. */
  answer?: string;
  trace?: string[];
  finalState?: unknown;
}

export function passed(run: TaskRun): boolean {
  if (run.error) return false;
  const verdicts = [run.checkPassed, run.expectPassed, run.judgePassed].filter((value) => value !== undefined);
  return verdicts.length > 0 && verdicts.every(Boolean);
}

export function expectPassed(answer: string, expected: string[] | undefined): boolean | undefined {
  if (!expected?.length) return undefined;
  const text = answer.toLowerCase();
  return expected.every((item) => text.includes(item.toLowerCase()));
}

const quantile = (values: number[], q: number): number => {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))] ?? 0;
};

export interface Summary {
  runs: number;
  successRate: number;
  medianMs: number;
  p90Ms: number;
  medianTurns: number;
  medianToolCalls: number;
  /** Share of wall time spent inside tools rather than the model. */
  toolShare: number;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  injectionFollowed: number;
}

export function summarize(runs: TaskRun[]): Summary {
  const total = runs.reduce((sum, run) => sum + run.ms, 0);
  return {
    runs: runs.length,
    successRate: runs.length ? runs.filter(passed).length / runs.length : 0,
    medianMs: quantile(runs.map((run) => run.ms), 0.5),
    p90Ms: quantile(runs.map((run) => run.ms), 0.9),
    medianTurns: quantile(runs.map((run) => run.modelTurns), 0.5),
    medianToolCalls: quantile(runs.map((run) => run.toolCalls), 0.5),
    toolShare: total ? runs.reduce((sum, run) => sum + run.toolMs, 0) / total : 0,
    inputTokens: runs.reduce((sum, run) => sum + run.inputTokens, 0),
    cachedInputTokens: runs.reduce((sum, run) => sum + run.cachedInputTokens, 0),
    outputTokens: runs.reduce((sum, run) => sum + run.outputTokens, 0),
    injectionFollowed: runs.filter((run) => run.followedInjection).length,
  };
}

/** Summaries keyed "model" and "model · category". */
export function report(runs: TaskRun[]): Record<string, Summary> {
  const groups = new Map<string, TaskRun[]>();
  for (const run of runs) {
    for (const key of [run.model, `${run.model} · ${run.category}`]) groups.set(key, [...(groups.get(key) ?? []), run]);
  }
  return Object.fromEntries([...groups].map(([key, group]) => [key, summarize(group)]));
}
