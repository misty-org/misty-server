import type { LanguageModelUsage } from "ai";

export interface ExecutionBudget {
  version: 0 | 1;
  remaining_ms: number;
  active: boolean;
  deadline?: string;
}

export function modelTimeout(budget: ExecutionBudget, now: number, legacyDeadline: number): number {
  // Existing admissions retain their absolute timeout policy while draining.
  if (budget.version === 0) {
    if (legacyDeadline <= now) throw new Error("agent_runtime_timeout");
    return Math.min(30 * 60_000, legacyDeadline - now);
  }
  const deadline = Date.parse(budget.deadline ?? "");
  if (budget.version !== 1 || !budget.active || !Number.isSafeInteger(budget.remaining_ms) || budget.remaining_ms > 30 * 60_000 || !Number.isFinite(deadline))
    throw new Error("invalid_execution_budget");
  const remaining = Math.min(budget.remaining_ms, deadline - now);
  if (remaining <= 0) throw new Error("agent_execution_time_limit");
  return remaining;
}

export function accumulateModelUsage(previous: LanguageModelUsage | undefined, next: LanguageModelUsage): LanguageModelUsage {
  if (!previous) return next;
  // Preserve unavailable details as unavailable; no synthetic token estimate.
  const sum = (left: number | undefined, right: number | undefined) => left === undefined && right === undefined ? undefined : (left ?? 0) + (right ?? 0);
  return {
    inputTokens: sum(previous.inputTokens, next.inputTokens),
    outputTokens: sum(previous.outputTokens, next.outputTokens),
    totalTokens: sum(previous.totalTokens, next.totalTokens),
    inputTokenDetails: {
      noCacheTokens: sum(previous.inputTokenDetails?.noCacheTokens, next.inputTokenDetails?.noCacheTokens),
      cacheReadTokens: sum(previous.inputTokenDetails?.cacheReadTokens, next.inputTokenDetails?.cacheReadTokens),
      cacheWriteTokens: sum(previous.inputTokenDetails?.cacheWriteTokens, next.inputTokenDetails?.cacheWriteTokens),
    },
    outputTokenDetails: {
      textTokens: sum(previous.outputTokenDetails?.textTokens, next.outputTokenDetails?.textTokens),
      reasoningTokens: sum(previous.outputTokenDetails?.reasoningTokens, next.outputTokenDetails?.reasoningTokens),
    },
  };
}
