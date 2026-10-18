import { describe, expect, it } from "vitest";
import { earlyGiveUp } from "../src/persistence-check.js";

const partial = (requirement: string, reason: string) => ({ outcome: "partial" as const, summary: "Some of it.", remaining: [{ requirement, reason }] });

describe("earlyGiveUp", () => {
  it("sends an early partial finish back to work", () => {
    expect(earlyGiveUp(partial("Tecovas boot options", "the search page returned an error"), 6, 120)).toContain("Tecovas boot options");
  });

  it("lets a finish stand when it is complete, late, or blocked on the person", () => {
    expect(earlyGiveUp({ outcome: "completed", summary: "Done.", remaining: [] }, 3, 120)).toBeUndefined();
    expect(earlyGiveUp(partial("Tecovas boot options", "the page failed"), 70, 120)).toBeUndefined();
    expect(earlyGiveUp(partial("Order history", "needs the person to sign in"), 3, 120)).toBeUndefined();
  });
});
