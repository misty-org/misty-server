import { expect, it } from "vitest";
import { modelTimeout } from "../src/model-budget.js";

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
