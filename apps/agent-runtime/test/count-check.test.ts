import { describe, expect, it } from "vitest";
import type { ModelMessage } from "ai";
import { countRefusal, requestedCounts } from "../src/count-check.js";
import { brokenProofTabs, missingSiteTabs } from "../src/proof-tabs.js";

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

describe("missing site tabs", () => {
  const program = (tabs: Array<{ id: string; url: string }>): ModelMessage => ({
    role: "tool",
    content: [{ type: "tool-result", toolCallId: "c", toolName: "browser_run", output: { type: "json", value: { tabs } as never } }],
  });
  const closed = (open: string): ModelMessage => ({
    role: "tool",
    content: [{ type: "tool-result", toolCallId: "d", toolName: "screen_close", output: { type: "text", value: `closed | Closed that tab. Your open tabs: ${open}.` } }],
  });
  const ask = "Start on Google, look up kits on RockAuto, then check Amazon for a tool and open the product page; leave those tabs open.";

  it("names a site the request asks for that the run visited and left with no tab open", () => {
    const visits = [
      program([{ id: "g", url: "https://www.google.com/search?q=x" }, { id: "r", url: "https://www.rockauto.com/en/catalog" }, { id: "a", url: "https://www.amazon.com/s?k=pliers" }]),
      closed("r"),
    ];
    expect(missingSiteTabs(ask, visits)).toEqual(["amazon"]);
    expect(missingSiteTabs(ask, visits.slice(0, 1))).toEqual([]);
    expect(missingSiteTabs("Check Amazon for a tool.", visits)).toEqual([]);
  });
});

