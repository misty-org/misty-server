import { describe, expect, it } from "vitest";
import type { ModelMessage } from "ai";
import { countRefusal, requestedCounts } from "../src/count-check.js";
import { brokenProofTabs } from "../src/proof-tabs.js";

const request = "Find at least 10 pediatricians in Raleigh who accept one of those plans, then find around 10 daycare centers, and give me 3 sections.";

describe("count check", () => {
  it("reads the counts a request asks for", () => {
    expect(requestedCounts(request)).toEqual([
      { count: 10, thing: "pediatricians" },
      { count: 10, thing: "daycare" },
    ]);
  });

  it("flags a section short of its count and lets a full one through", () => {
    const rows = (n: number) => Array.from({ length: n }, (_, index) => `- Item ${index + 1}`).join("\n");
    const short = `## Pediatricians\n${rows(8)}\n\n## Daycare centers\n${rows(10)}`;
    expect(countRefusal(request, short)).toMatch(/10 pediatricians and your answer lists 8/);
    expect(countRefusal(request, `## Pediatricians\n${rows(10)}\n\n## Daycare centers\n${rows(9)}`)).toBeUndefined();
    expect(countRefusal(request, "No sections at all.")).toBeUndefined();
  });
});

describe("broken proof tabs", () => {
  const program = (value: unknown): ModelMessage => ({
    role: "tool",
    content: [{ type: "tool-result", toolCallId: "c", toolName: "browser_run", output: { type: "json", value: value as never } }],
  });
  it("names kept tabs that end on an error page", () => {
    const messages = [
      program({ kept: ["a", "b"], tabs: [{ id: "a", url: "https://aetna.com/x", title: "We Can't Find That Page | Aetna" }, { id: "b", url: "https://uhc.com", title: "UnitedHealthcare" }] }),
    ];
    expect(brokenProofTabs(messages)).toEqual(['https://aetna.com/x ("We Can\'t Find That Page | Aetna")']);
    expect(brokenProofTabs([program({ kept: ["b"], tabs: [{ id: "b", url: "https://uhc.com", title: "UnitedHealthcare" }] })])).toEqual([]);
  });
});
