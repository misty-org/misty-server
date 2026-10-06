import { expect, it } from "vitest";
import { accumulateModelUsage, modelTimeout, modelTurnLimit } from "../src/model-budget.js";
import type { LanguageModelUsage } from "ai";

it("uses the smaller authoritative deadline and remaining allowance", () => {
  const now=Date.parse("2026-09-07T12:00:00Z");
  expect(modelTimeout({ version: 1, remaining_ms: 2000, active: true, deadline: new Date(now+1000).toISOString() },now,now+5000)).toBe(1000);
  expect(modelTimeout({ version: 1, remaining_ms: 500, active: true, deadline: new Date(now+1000).toISOString() },now,now+5000)).toBe(500);
  expect(() => modelTimeout({ version: 1, remaining_ms: 500, active: true, deadline: new Date(now-1).toISOString() },now,now+5000)).toThrow("agent_execution_time_limit");
});

it("does not replace missing or invalid new-budget responses with an unlimited model call", () => {
  const now=Date.now();
  for (const value of [{}, { version: 1, active: false, remaining_ms: 10 }, { version: 1, active: true, remaining_ms: 1_800_001, deadline: new Date(now+2000).toISOString() }]) {
    expect(() => modelTimeout(value as never,now,now+5000)).toThrow("invalid_execution_budget");
  }
});

it("rejects invalid turn budgets and preserves legacy default", () => {
  expect(modelTurnLimit(undefined)).toBe(20);
  expect(modelTurnLimit(120)).toBe(120);
  for (const value of [0, -1, 121, 1.5, NaN, Infinity, null, "120"]) {
    expect(() => modelTurnLimit(value as number)).toThrow("invalid_model_turn_limit");
  }
});

it("preserves unknown aggregate details across root and nested visual calls", () => {
  const root: LanguageModelUsage = {inputTokens: 100, outputTokens: 10, totalTokens: 110, inputTokenDetails: {noCacheTokens: undefined, cacheReadTokens: undefined, cacheWriteTokens: undefined}, outputTokenDetails: {textTokens: undefined, reasoningTokens: undefined}};
  const visual: LanguageModelUsage = {inputTokens: 200, outputTokens: 20, totalTokens: 220, inputTokenDetails: {noCacheTokens: undefined, cacheWriteTokens: undefined, cacheReadTokens: 150}, outputTokenDetails: {textTokens: undefined, reasoningTokens: 5}};
  for (const [first, second] of [[root, visual], [visual, root]] as const) {
    const combined = accumulateModelUsage(first, second);
    expect(combined.inputTokens).toBe(300);
    expect(combined.outputTokens).toBe(30);
    expect(combined.inputTokenDetails.cacheReadTokens).toBeUndefined();
    expect(combined.outputTokenDetails.reasoningTokens).toBeUndefined();
    const next = accumulateModelUsage(combined, visual);
    expect(next.inputTokens).toBe(500);
    expect(next.inputTokenDetails.cacheReadTokens).toBeUndefined();
  }
});

it("sums complete usage exactly, including explicit zero detail counters", () => {
  const first: LanguageModelUsage = {inputTokens: 100, outputTokens: 10, totalTokens: 110, inputTokenDetails: {noCacheTokens: undefined, cacheWriteTokens: undefined, cacheReadTokens: 0}, outputTokenDetails: {textTokens: undefined, reasoningTokens: 0}};
  const next: LanguageModelUsage = {inputTokens: 200, outputTokens: 20, totalTokens: 220, inputTokenDetails: {noCacheTokens: undefined, cacheWriteTokens: undefined, cacheReadTokens: 150}, outputTokenDetails: {textTokens: undefined, reasoningTokens: 5}};
  const combined = accumulateModelUsage(first, next);
  expect(combined.inputTokenDetails.cacheReadTokens).toBe(150);
  expect(combined.outputTokenDetails.reasoningTokens).toBe(5);
  expect(combined.totalTokens).toBe(330);
  expect(accumulateModelUsage(undefined, next)).toBe(next);
});
