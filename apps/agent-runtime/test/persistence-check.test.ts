import { describe, expect, it } from "vitest";
import { MAX_PERSIST, earlyGiveUp, persistAgain } from "../src/persistence-check.js";

const partial = (requirement: string, reason: string) => ({ outcome: "partial" as const, summary: "Some of it.", remaining: [{ requirement, reason }] });

describe("earlyGiveUp", () => {
  it("sends an early partial finish back to work", () => {
    expect(earlyGiveUp(partial("Tecovas boot options", "the search page returned an error"), 6, 120)).toContain("Tecovas boot options");
  });

  it("lets a finish stand when it is complete, late, or blocked on the person", () => {
    expect(earlyGiveUp({ outcome: "completed", summary: "Done.", remaining: [] }, 3, 120)).toBeUndefined();
    expect(earlyGiveUp(partial("Tecovas boot options", "the page failed"), 70, 120)).toContain("Tecovas boot options");
    expect(earlyGiveUp(partial("Tecovas boot options", "the page failed"), 111, 120)).toBeUndefined();
    expect(earlyGiveUp(partial("Order history", "needs the person to sign in"), 3, 120)).toBeUndefined();
  });
});

describe("persistAgain", () => {
  it("sends a later give-up back only while the run keeps finding new things", () => {
    const quit = partial("Ten Raleigh pediatricians verified", "only four profiles were read");
    expect(persistAgain(quit, 37, 200, 0, 5)).toContain("Ten Raleigh pediatricians");
    expect(persistAgain(quit, 37, 200, 0, 1)).toBeUndefined();
    expect(persistAgain(quit, 37, 200, MAX_PERSIST, 9)).toBeUndefined();
    expect(persistAgain(quit, 195, 200, 0, 9)).toBeUndefined();
  });
});
