import type { ModelMessage } from "ai";
import { describe, expect, it } from "vitest";
import { ledgerNote, openPages, parseGaps, reviewRefusal, worthLedger, worthReviewing } from "../src/requirements-review.js";

const done = { outcome: "completed" as const, summary: "Here is the memo.", remaining: [] };
const long = "Compare eight cleats on price, studs, weight and returns, and keep each product page open. ".repeat(4);

describe("requirements review", () => {
  it("reviews long requests while turns remain", () => {
    expect(worthReviewing(long, done, 20, 90)).toBe(true);
    expect(worthReviewing("What time is it in Lisbon?", done, 2, 90)).toBe(false);
    expect(worthReviewing(long, done, 87, 90)).toBe(false);
    expect(worthReviewing(long, { ...done, outcome: "blocked" }, 20, 90)).toBe(false);
  });

  it("lists a long request's requirements before work starts", () => {
    expect(worthLedger(long)).toBe(true);
    expect(worthLedger("What time is it in Lisbon?")).toBe(false);
    expect(ledgerNote("1. Eight cleats\n2. Price for each")).toContain("2. Price for each");
  });

  it("reads gaps one per line, and NONE as none", () => {
    expect(parseGaps("NONE")).toEqual([]);
    expect(parseGaps(" none. ")).toEqual([]);
    expect(parseGaps("Gaps:\n- Weight for the Puma Future\n- The Nike page is not open\nThanks")).toEqual(["Weight for the Puma Future", "The Nike page is not open"]);
    expect(reviewRefusal(["Weight for the Puma Future"])).toContain("- Weight for the Puma Future");
  });

  it("lists the page each own tab shows last", () => {
    const messages = [
      { role: "assistant", content: [{ type: "tool-call", toolCallId: "a", toolName: "screen_open", input: { url: "https://nike.com/phantom" } }] },
      { role: "tool", content: [{ type: "tool-result", toolCallId: "a", toolName: "screen_open", output: { type: "text", value: "Your own browser tab is open (scopeId agent-scope-1a2b)." } }] },
      { role: "assistant", content: [{ type: "tool-call", toolCallId: "b", toolName: "browser_navigate", input: { scopeId: "agent-scope-1a2b", url: "https://nike.com/phantom?size=10" } }] },
      { role: "assistant", content: [{ type: "tool-call", toolCallId: "c", toolName: "browser_navigate", input: { scopeId: "agent-scope-9z", url: "https://ultimate.org/cleats" } }] },
    ] as ModelMessage[];
    expect(openPages(messages, { open: "screen_open", navigate: "browser_navigate" })).toEqual(["https://nike.com/phantom?size=10", "https://ultimate.org/cleats"]);
  });
});
