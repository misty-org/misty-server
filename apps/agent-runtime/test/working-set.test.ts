import { describe, expect, it } from "vitest";
import type { ModelMessage } from "ai";
import { cutOldResults, DIGEST_MARK } from "../src/working-set.js";
import { loopNote } from "../src/loop-guard.js";

const call = (id: string, toolName: string, input: unknown): ModelMessage => ({
  role: "assistant",
  content: [{ type: "tool-call", toolCallId: id, toolName, input }],
});
const result = (id: string, toolName: string, value: string): ModelMessage => ({
  role: "tool",
  content: [{ type: "tool-result", toolCallId: id, toolName, output: { type: "text", value } }],
});
const text = (message: ModelMessage) =>
  message.role === "tool" && message.content[0]?.type === "tool-result" && message.content[0].output.type === "text"
    ? message.content[0].output.value
    : "";

describe("the working set", () => {
  it("keeps the newest two pages and four readings whole, digests older reading, cuts older pages", () => {
    const page = JSON.stringify({ tree: "x".repeat(5_000) });
    const found = "Aetna, Blue Cross NC and UnitedHealthcare sell plans in Raleigh. " + "detail ".repeat(400);
    const messages: ModelMessage[] = [
      call("f0", "web_fetch", { url: "https://example.com/0" }),
      result("f0", "web_fetch", found),
      ...Array.from({ length: 4 }, (_, index) => [call(`f${index + 1}`, "web_fetch", {}), result(`f${index + 1}`, "web_fetch", found)]).flat(),
      ...Array.from({ length: 4 }, (_, index) => [call(`p${index}`, "browser_snapshot", {}), result(`p${index}`, "browser_snapshot", page)]).flat(),
    ];
    const { messages: out, cut } = cutOldResults(messages);
    expect(cut).toBe(3);
    expect(text(out[1]!).startsWith(DIGEST_MARK)).toBe(true);
    expect(text(out[1]!)).toContain("Aetna, Blue Cross NC and UnitedHealthcare");
    expect(text(out[1]!).length).toBeLessThan(800);
    expect(text(out[3]!)).toBe(found);
    expect(text(out[11]!)).toMatch(/^\[Older result cut/);
    expect(text(out[13]!)).toMatch(/^\[Older result cut/);
    expect(text(out[15]!)).toBe(page);
    expect(text(out[17]!)).toBe(page);
    expect(cutOldResults(out).cut).toBe(0);
  });
});

describe("the loop guard", () => {
  it("says nothing for ordinary progress", () => {
    const messages = ["a", "b", "c"].map((page, index) => call(`${index}`, "browser_navigate", { url: `https://shop.example/${page}` }));
    expect(loopNote(messages)).toBeUndefined();
  });

  it("notes the same address opened three times, once", () => {
    const messages: ModelMessage[] = [
      call("1", "screen_open", { url: "https://www.usnews.com/rankings/" }),
      call("2", "web_fetch", { url: "https://www.usnews.com/rankings" }),
      call("3", "browser_navigate", { url: "https://www.usnews.com/rankings#top" }),
    ];
    const note = loopNote(messages);
    expect(note).toMatch(/opened https:\/\/usnews\.com\/rankings 3 times/);
    messages.push({ role: "user", content: note! });
    expect(loopNote(messages)).toBeUndefined();
  });
});

import { asksForOpenPages, proofTabsRefusal } from "../src/proof-tabs.js";

describe("proof tabs", () => {
  it("recognises requests to leave pages open", () => {
    expect(asksForOpenPages("open at least one qualifying listing in its own tab so I can see the photos")).toBe(true);
    expect(asksForOpenPages("keep that product page open too so I can compare")).toBe(true);
    expect(asksForOpenPages("leave the directory tabs open as visual proof")).toBe(true);
    expect(asksForOpenPages("What is the capital of France?")).toBe(false);
  });

  it("sends a finish back once to list tabs, not when they were just listed", () => {
    const request = "Keep the PIPS scholarship page open in another tab.";
    expect(proofTabsRefusal(request, [], "browser_tab_list")).toMatch(/call browser_tab_list/);
    const listed: ModelMessage[] = [
      { role: "assistant", content: [{ type: "tool-call", toolCallId: "t", toolName: "browser_tab_list", input: {} }] },
    ];
    expect(proofTabsRefusal(request, listed, "browser_tab_list")).toBeUndefined();
  });
});

import { condense, workingSet } from "../src/condenser.js";

describe("condensing with a cached provider", () => {
  it("trims by the transcript's size even when the last call reported a small uncached input", async () => {
    const page = JSON.stringify({ tree: "x".repeat(20_000) });
    const messages: ModelMessage[] = Array.from({ length: 12 }, (_, index) => [
      call(`c${index}`, "browser_snapshot", {}),
      result(`c${index}`, "browser_snapshot", page),
    ]).flat();
    const out = await condense(messages, { inputTokens: 3_000, sentBytes: 1_000, window: 200_000 }, [workingSet]);
    const cut = out.filter((message) => text(message).startsWith("[Older result cut")).length;
    expect(cut).toBe(10);
  });

  it("holds a small cut back so the cached prefix survives", async () => {
    const page = JSON.stringify({ tree: "x".repeat(9_000) });
    const messages: ModelMessage[] = Array.from({ length: 6 }, (_, index) => [
      call(`c${index}`, "browser_snapshot", {}),
      result(`c${index}`, "browser_snapshot", page),
    ]).flat();
    const out = await condense(messages, { inputTokens: 30_000, sentBytes: 60_000, window: 200_000 }, [workingSet]);
    expect(out).toBe(messages);
  });
});
