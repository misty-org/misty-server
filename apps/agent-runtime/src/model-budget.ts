import type { LanguageModelUsage } from "ai";

export interface ExecutionBudget {
  version: 1;
  remaining_ms: number;
  active: boolean;
  deadline?: string;
}

/** The most execution Misty grants a run: four hours for a development eval, 30 minutes otherwise
 * (plan_models.go, evalExecutionLimitMS). */
const MAX_EXECUTION_MS = 4 * 60 * 60_000;

export function modelTimeout(budget: ExecutionBudget, now: number): number {
  const deadline = Date.parse(budget.deadline ?? "");
  if (budget.version !== 1 || !budget.active || !Number.isSafeInteger(budget.remaining_ms) || budget.remaining_ms > MAX_EXECUTION_MS || !Number.isFinite(deadline))
    throw new Error("invalid_execution_budget");
  const remaining = Math.min(budget.remaining_ms, deadline - now);
  if (remaining <= 0) throw new Error("agent_execution_time_limit");
  return remaining;
}

export function accumulateModelUsage(previous: LanguageModelUsage | undefined, next: LanguageModelUsage): LanguageModelUsage {
  if (!previous) return next;
  // A total is authoritative only when every contributing call reported it.
  // Root SDK aggregates may omit details that nested visual calls do report;
  // adding the known subset would falsely contradict the per-call receipts.
  const sum = (left: number | undefined, right: number | undefined) => left === undefined || right === undefined ? undefined : left + right;
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

// Misty sends each run's turn limit (agent_runtime_internal.go); a context without one gets 20 turns.
export function modelTurnLimit(value: number | undefined): number {
  if (value === undefined) return 20;
  // 200 is a development eval's most (docs/agents/harness.md, Errors and budget); plans stay below.
  if (!Number.isSafeInteger(value) || value < 1 || value > 200) throw new Error("invalid_model_turn_limit");
  return value;
}
