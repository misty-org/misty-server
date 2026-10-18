import type { ModelMessage } from "ai";
import { describe, expect, it } from "vitest";
import { closedResearchTools, researchLimits, researchNote, researchUse, sameQuery } from "../src/research-budget.js";

const tools = { search: "web_search", fetch: "web_fetch" };

function calls(...items: Array<[string, Record<string, string>]>): ModelMessage[] {
  return items.map(([toolName, input], index) => ({
    role: "assistant",
    content: [{ type: "tool-call", toolCallId: `c${index}`, toolName, input }],
  })) as ModelMessage[];
}
const search = (query: string): [string, Record<string, string>] => ["web_search", { query }];
const fetchPage = (url: string): [string, Record<string, string>] => ["web_fetch", { url }];

describe("research budget", () => {
  it("stays open for a handful of distinct calls", () => {
    const use = researchUse(calls(search("prime time anthropic video"), fetchPage("https://youtube.com/watch?v=1")), tools);
    expect(use).toMatchObject({ searches: 1, fetches: 1, repeats: 0, cost: 2, stage: "open" });
  });

  it("counts a reworded search and a reread page as repeats that cost double", () => {
    const use = researchUse(
      calls(search("ThePrimeagen anthropic no cruel language video"), search("theprimeagen anthropic cruel language"), fetchPage("https://a.com/x#top"), fetchPage("https://a.com/x/")),
      tools,
    );
    expect(use.repeats).toBe(2);
    expect(use.cost).toBe(6);
    expect(use.stage).toBe("wrap-up");
  });

  it("does not call different questions repeats", () => {
    expect(sameQuery(new Set(["rust", "async", "runtime"]), new Set(["postgres", "pricing", "tiers"]))).toBe(false);
  });

  it("closes at the limit, hides the research tools, and ignores other tools", () => {
    const many = calls(...Array.from({ length: researchLimits.close }, (_, i) => fetchPage(`https://site${i}.com`)), ["browser_act", { goal: "x" }]);
    const use = researchUse(many, tools);
    expect(use.stage).toBe("closed");
    expect([...closedResearchTools(use, tools)].sort()).toEqual(["web_fetch", "web_search"]);
    expect(closedResearchTools(researchUse([], tools), tools).size).toBe(0);
  });

  it("says each stage once, from the transcript", () => {
    const messages = calls(...Array.from({ length: researchLimits.wrapUp }, (_, i) => fetchPage(`https://site${i}.com`)));
    const use = researchUse(messages, tools);
    const note = researchNote(use, messages)!;
    expect(note).toMatch(/^Research budget \(wrap-up\)/);
    expect(researchNote(use, [...messages, { role: "user", content: note }])).toBeUndefined();
    expect(researchNote(researchUse([], tools), [])).toBeUndefined();
  });

  it("does nothing when the run has no research tools", () => {
    expect(researchUse(calls(search("a b c")), {}).cost).toBe(0);
  });
  it("points a run that can open its own tab at it for facts a fetch can't show", () => {
    const tools = { search: "web_search", fetch: "web_fetch", open: "screen_open" };
    const use = { searches: 3, fetches: 3, repeats: 0, cost: 6, stage: "wrap-up" as const };
    expect(researchNote(use, [], tools)).toContain("open them in your own tab with screen_open");
    expect(researchNote(use, [], { search: "web_search", fetch: "web_fetch" })).not.toContain("screen_open");
  });
  it("gives a long, planned request a longer budget", () => {
    const tools = { search: "web_search", fetch: "web_fetch" };
    const searches = Array.from({ length: 8 }, (_, index) => ({ role: "assistant" as const, content: [{ type: "tool-call" as const, toolCallId: `s${index}`, toolName: "web_search", input: { query: ["notion pricing", "linear plans", "airtable cost", "depop seller", "hulu shows", "memory alpha", "ponies cast", "old navy joggers"][index] } }] }));
    expect(researchUse(searches, tools).stage).toBe("wrap-up");
    const plan = { role: "assistant" as const, content: [{ type: "tool-call" as const, toolCallId: "plan", toolName: "log_work_checklist", input: { items: ["a", "b", "c", "d", "e", "f"].map((id) => ({ id, title: id, status: "pending" })) } }] };
    expect(researchUse([plan, ...searches], tools).stage).toBe("open");
  });
});
