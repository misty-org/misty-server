import type { ModelMessage } from "ai";
import { describe, expect, it } from "vitest";
import { citationRefusal, citedPages, readPages, unreadCitations } from "../src/citation-check.js";

const tools = { search: "web_search", fetch: "web_fetch" };

function call(id: string, toolName: string, input: unknown, output: unknown): ModelMessage[] {
  return [
    { role: "assistant", content: [{ type: "tool-call", toolCallId: id, toolName, input }] },
    { role: "tool", content: [{ type: "tool-result", toolCallId: id, toolName, output }] },
  ] as ModelMessage[];
}
const found = (id: string, ...urls: string[]) =>
  call(id, "web_search", { query: "x" }, { type: "json", value: { results: urls.map((url) => ({ url, snippet: "..." })) } });

describe("citation grounding", () => {
  it("finds cited pages with or without a scheme, ignoring sentence punctuation", () => {
    const pages = [...citedPages("See https://a.com/x. Also kprofiles.com/a2o/members/, and (b.org/p).").values()];
    expect(pages).toEqual(["https://a.com/x", "kprofiles.com/a2o/members/", "b.org/p"]);
  });

  it("flags a page seen only in search results, and accepts it once fetched", () => {
    const search = found("s", "https://kprofiles.com/a2o/");
    expect(unreadCitations("Source: kprofiles.com/a2o/", search, tools)).toEqual(["kprofiles.com/a2o/"]);
    const read = [...search, ...call("f", "web_fetch", { url: "https://kprofiles.com/a2o" }, { type: "json", value: { text: "members" } })];
    expect(unreadCitations("Source: kprofiles.com/a2o/", read, tools)).toEqual([]);
  });

  it("does not count a failed fetch as reading", () => {
    const messages = [...found("s", "https://a.com/p"), ...call("f", "web_fetch", { url: "https://a.com/p" }, { type: "error-text", value: "blocked" })];
    expect(readPages(messages, tools).has("a.com/p")).toBe(false);
    expect(unreadCitations("https://a.com/p", messages, tools)).toEqual(["https://a.com/p"]);
  });

  it("leaves links from app tools and from the person alone", () => {
    const messages = [
      { role: "user", content: "Summarize https://rfc.org/1" },
      ...call("g", "integration_execute", { tool_slug: "GITHUB_CREATE_ISSUE" }, { type: "json", value: { html_url: "https://github.com/o/r/issues/1" } }),
    ] as ModelMessage[];
    expect(unreadCitations("Opened https://github.com/o/r/issues/1 from https://rfc.org/1", messages, tools)).toEqual([]);
  });

  it("counts a page opened in a browser as read", () => {
    const messages = [...found("s", "https://a.com/p"), ...call("b", "browser_navigate", { url: "https://a.com/p" }, { type: "json", value: {} })];
    expect(unreadCitations("https://a.com/p", messages, tools)).toEqual([]);
  });

  it("words the refusal for open and closed research", () => {
    expect(citationRefusal(["a.com/p"], true)).toMatch(/Read each with web_fetch/);
    expect(citationRefusal(["a.com/p"], false)).toMatch(/closed for this run/);
    expect(citationRefusal(["a.com/p"], true)).toMatch(/call agent_finish again\.$/);
    expect(citationRefusal(["a.com/p"], true, "give your answer again")).toMatch(/give your answer again\.$/);
  });
});
