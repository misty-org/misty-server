import type { ModelMessage } from "ai";
import { describe, expect, it } from "vitest";
import { address, loopNote } from "../src/loop-guard.js";

let id = 0;
/** One turn: the call and its result. */
function turn(toolName: string, input: unknown, output: unknown = { type: "json", value: { ok: true } }): ModelMessage[] {
  const toolCallId = `call-${id++}`;
  return [
    { role: "assistant", content: [{ type: "tool-call", toolCallId, toolName, input }] },
    { role: "tool", content: [{ type: "tool-result", toolCallId, toolName, output }] },
  ] as ModelMessage[];
}

const fetches = (...urls: string[]) => urls.flatMap((url) => turn("web_fetch", { url }));

describe("loopNote", () => {
  it("says nothing for ordinary research", () => {
    expect(loopNote(fetches("https://a.com/1", "https://b.com/2", "https://c.com/3", "https://a.com/4"))).toBeUndefined();
  });

  it("catches one address opened three times however it was written, across the whole run", () => {
    const filler = Array.from({ length: 14 }, (_, index) => turn("web_search", { query: `q${index}` })).flat();
    const messages = [
      ...fetches("https://www.hypebeast.com/2026/10/palace-winter?utm_source=x"),
      ...filler,
      ...fetches("http://hypebeast.com/2026/10/palace-winter/", "https://hypebeast.com/2026/10/palace-winter#top"),
    ];
    expect(loopNote(messages)).toMatch(/opened https:\/\/hypebeast\.com\/2026\/10\/palace-winter 3 times/);
  });

  it("catches most of the last pages on one site, but not on a search engine", () => {
    const articles = fetches(...Array.from({ length: 6 }, (_, index) => `https://hypebeast.com/2026/10/article-${index}`));
    expect(loopNote(articles)).toMatch(/6 of the last 6 pages you opened are on hypebeast\.com/);
    const results = Array.from({ length: 6 }, (_, index) => turn("browser_navigate", { url: `https://www.google.com/search?q=${index}` })).flat();
    expect(loopNote(results)).toBeUndefined();
  });

  it("catches a call that failed twice, and the same call three times", () => {
    const error = { type: "error-text", value: "no element" };
    expect(loopNote([...turn("browser_steps", { ref: "e4" }, error), ...turn("browser_steps", { ref: "e4" }, error)])).toMatch(/failed 2 times/);
    expect(loopNote([1, 2, 3].flatMap(() => turn("browser_snapshot", { scopeId: "s" })))).toMatch(/browser_snapshot with the same input 3 times/);
  });

  it("says each pattern once", () => {
    const messages = fetches("https://a.com/x", "https://a.com/x", "https://a.com/x");
    const note = loopNote(messages)!;
    expect(loopNote([...messages, { role: "user", content: note }, ...fetches("https://a.com/x")])).toBeUndefined();
  });
});

describe("address", () => {
  it("ignores scheme, www, fragments, trailing slashes and tracking parameters", () => {
    expect(address({ url: "http://WWW.Example.com/a/?utm_medium=y&id=2#s" })).toBe("https://example.com/a?id=2");
    expect(address({ url: "example.com/a" })).toBe("https://example.com/a");
    expect(address({})).toBe("");
  });
});
