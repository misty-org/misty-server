import { describe, expect, it } from "vitest";
import { normalizeSearchOutput } from "../src/web-search.js";

describe("web search results", () => {
  it("reads each engine's results in one shape", () => {
    expect(normalizeSearchOutput("parallel", {
      searchId: "s", results: [{ url: "https://a.example/x", title: "A", excerpt: "about a", publishDate: "2026-01-02" }],
    })).toEqual([{ title: "A", url: "https://a.example/x", snippet: "about a", published: "2026-01-02" }]);
    expect(normalizeSearchOutput("perplexity", {
      id: "p", results: [{ url: "https://b.example", title: "B", snippet: "about b", date: "2026-02-03" }],
    })).toEqual([{ title: "B", url: "https://b.example", snippet: "about b", published: "2026-02-03" }]);
    expect(normalizeSearchOutput("exa", {
      requestId: "e", results: [{ id: "1", url: "https://c.example", title: "", highlights: ["one", "two"] }],
    })).toEqual([{ title: "https://c.example", url: "https://c.example", snippet: "one … two" }]);
  });

  it("drops non-web links and keeps the requested count", () => {
    const results = normalizeSearchOutput("parallel", {
      results: [
        { url: "javascript:alert(1)", title: "x", excerpt: "" },
        { url: "https://1.example", title: "1", excerpt: "" },
        { url: "https://2.example", title: "2", excerpt: "" },
      ],
    }, 1);
    expect(results.map((result) => result.url)).toEqual(["https://1.example"]);
  });

  it("fails without echoing an engine error", () => {
    expect(() => normalizeSearchOutput("perplexity", { error: "api_error", message: "key sk-123 rejected" }))
      .toThrow("The web search did not return results.");
  });
});
