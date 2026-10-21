import { describe, expect, it } from "vitest";
import { budgetCalls, flushPoint, TEACH_BUDGET_SPENT } from "../src/teach-turn.js";

describe("teach turn streaming", () => {
  it("sends whole sentences and lines, keeping the rest", () => {
    const buffer = "[ROUTE:new:two sum]\nlet's start. make an empty dict";
    const cut = flushPoint(buffer);
    expect(buffer.slice(0, cut)).toBe("[ROUTE:new:two sum]\nlet's start. ");
    expect(buffer.slice(cut)).toBe("make an empty dict");
  });

  it("does not split numbers or a sentence still being written", () => {
    expect(flushPoint("it runs in 3.5 seconds")).toBe(0);
    expect(flushPoint("is 7 in the dictionary?")).toBe(0);
    expect(flushPoint("is 7 in the dictionary? no")).toBe("is 7 in the dictionary? ".length);
  });
});

describe("teach turn lookups", () => {
  it("runs calls the budget covers and refuses the rest", () => {
    const calls = [
      { toolCallId: "a", toolName: "web_search", input: { query: "react useEffect" } },
      { toolCallId: "b", toolName: "web_fetch", input: { url: "https://react.dev" } },
      { toolCallId: "c", toolName: "browser_act", input: {} },
    ];
    expect(budgetCalls(calls, 1).map((c) => c.runAs)).toEqual(["web.search", undefined, undefined]);
    expect(budgetCalls(calls, 4).map((c) => c.runAs)).toEqual(["web.search", "web.fetch", undefined]);
    expect(TEACH_BUDGET_SPENT.error).toMatch(/used up/);
  });
});
