import { describe, expect, it } from "vitest";
import { withRuntimeInstructions } from "../src/context/prompt-cache.js";

describe("withRuntimeInstructions", () => {
  it("puts the fixed instructions before the part that changes every run", () => {
    const system = "Static prompt.\n\nAuthoritative run context:\n- Current time: 2026-10-15T21:00:00Z\nCurrent tab: x";
    const result = withRuntimeInstructions(system, ["Execution rules.", "Finish with a report."]);
    expect(result.indexOf("Execution rules.")).toBeLessThan(result.indexOf("Authoritative run context:"));
    expect(result.startsWith("Static prompt.")).toBe(true);
    expect(result.endsWith("Current tab: x")).toBe(true);
  });

  it("appends when there is nothing volatile to move past", () => {
    expect(withRuntimeInstructions("Static.", ["Rules."])).toBe("Static.\n\nRules.");
  });
});
