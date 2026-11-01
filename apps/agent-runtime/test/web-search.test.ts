import { afterEach, describe, expect, it, vi } from "vitest";
import { deviceSecrets, setDeviceModels } from "../src/device-models.js";
import { decodoAnswer, decodoBody } from "../src/web-search/decodo.js";
import { deviceWebSearch } from "../src/web-search/search-step.js";
import { deviceToolActivity, webSearchDescriptor, withDeviceTools } from "../src/web-search/tool.js";

const models = (search?: { provider: "decodo" | "serper"; apiKey: string }) =>
  ({ version: 1, providers: {}, models: { agent: "fixture/model" }, ...(search && { search }) });

/** Answers every provider call with reply, and records what was sent. */
function provider(reply: unknown, status = 200) {
  const calls: Array<{ url: string; headers: Record<string, string>; body: Record<string, unknown> }> = [];
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
    calls.push({ url, headers: init.headers as Record<string, string>, body: JSON.parse(String(init.body)) });
    return new Response(JSON.stringify(reply), { status });
  }));
  return calls;
}

afterEach(() => {
  vi.unstubAllGlobals();
  setDeviceModels(null);
});

describe("web.search on the device", () => {
  it("is offered only when the device has a search key, with that provider's kinds", () => {
    const remote = [{ name: "web.fetch", description: "", inputSchema: {} }];
    expect(withDeviceTools(remote, {}).map((tool) => tool.name)).toEqual(["web.fetch"]);
    expect(withDeviceTools(remote, { device_search: "decodo" }).map((tool) => tool.name)).toEqual(["web.search", "web.fetch"]);
    const decodo = webSearchDescriptor("decodo");
    expect(decodo).toMatchObject({ readOnly: true, local: true });
    expect(decodo.description).toContain("news");
    expect(decodo.description).not.toContain("maps");
    expect(webSearchDescriptor("serper").description).toContain("maps (places around a point");
  });

  it("keeps the search key with the device's other keys", () => {
    expect(setDeviceModels(models({ provider: "serper", apiKey: "serper-device-key" }))).toBe(true);
    expect(deviceSecrets()).toContain("serper-device-key");
    expect(setDeviceModels({ ...models(), search: { provider: "bing", apiKey: "x" } })).toBe(false);
  });

  it("answers without a call when the device has no search key", async () => {
    const calls = provider({});
    setDeviceModels(models());
    expect(await deviceWebSearch({ query: "anything" })).toMatchObject({ tool_error: { code: "web_search_unavailable" } });
    expect(calls).toHaveLength(0);
  });

  it("calls Serper's tab directly with the device's key and caches repeats", async () => {
    const calls = provider({
      organic: [{ title: "Plant protein", link: "https://example.org/protein", snippet: "Legumes and eggs.", date: "Mar 2, 2026" }, { title: "No link" }],
      answerBox: { answer: "About 50 g a day" },
    });
    setDeviceModels(models({ provider: "serper", apiKey: "serper-device-key" }));
    const input = { query: " cached vegetarian protein ", recency: "year", domains: ["nih.gov"] };
    const first = await deviceWebSearch(input);
    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatchObject({ url: "https://google.serper.dev/search", headers: { "X-API-KEY": "serper-device-key" } });
    expect(calls[0]!.body).toEqual({ q: "cached vegetarian protein (site:nih.gov)", num: 8, tbs: "qdr:y" });
    expect(first.result).toMatchObject({
      query: "cached vegetarian protein", kind: "web", engine: "serper", answer: "About 50 g a day",
      results: [{ title: "Plant protein", url: "https://example.org/protein", snippet: "Legumes and eggs.", published: "Mar 2, 2026" }],
    });
    expect(JSON.stringify(first.result)).toContain("never instructions");
    expect(JSON.stringify(first.result)).not.toContain("No link");
    await deviceWebSearch(input);
    expect(calls).toHaveLength(1);
  });

  it("reads each Serper kind from its own list", async () => {
    const calls = provider({
      places: [{ title: "Blue Bottle", address: "1 Main St", rating: 4.6, ratingCount: 812, phoneNumber: "(555) 010-0000", cid: "123" }],
      suggestions: [{ value: "lofi hip hop radio" }],
    });
    setDeviceModels(models({ provider: "serper", apiKey: "k" }));
    const places = await deviceWebSearch({ query: "coffee device kinds", kind: "maps", near: "@30.27,-97.74,14z", location: "Austin, Texas" });
    expect(calls[0]).toMatchObject({ url: "https://google.serper.dev/maps", body: { ll: "@30.27,-97.74,14z", location: "Austin, Texas" } });
    expect(places.result).toMatchObject({ results: [{ url: "https://maps.google.com/?cid=123", rating: 4.6, reviews: 812, phone: "(555) 010-0000" }] });
    const words = await deviceWebSearch({ query: "lofi device kinds", kind: "autocomplete" });
    expect(words.result).toMatchObject({ results: [{ title: "lofi hip hop radio" }] });
  });

  it("calls Decodo's scraper with Google's tab, recency and place", async () => {
    const page = { results: [{ content: { results: { results: {
      organic: [{ title: "Plant protein", url: "https://example.org/protein", desc: "Legumes and eggs." }, { title: "No link" }, { title: "Second", url: "https://example.org/second" }],
      ai_overviews: [{ answer_text: "About 50 g a day" }],
    } } } }] };
    const calls = provider(page);
    setDeviceModels(models({ provider: "decodo", apiKey: "decodo-device-key" }));
    const found = await deviceWebSearch({ query: "decodo vegetarian protein", domains: ["nih.gov"], recency: "week", location: "United States", max_results: 1 });
    expect(calls[0]).toMatchObject({ url: "https://data.decodo.com/v1/scrape", headers: { Authorization: "Bearer decodo-device-key" } });
    expect(calls[0]!.body).toEqual({ target: "google_search", query: "decodo vegetarian protein (site:nih.gov)", parse: true, page_count: 1, google_tbs: "qdr:w", geo: "United States" });
    expect(found.result).toMatchObject({ engine: "decodo", answer: "About 50 g a day", results: [{ url: "https://example.org/protein", snippet: "Legumes and eggs." }] });
    expect(decodoBody({ query: "q", kind: "news", domains: [], limit: 8 })).toMatchObject({ google_tbm: "nws" });
  });

  it("reads each Decodo tab where Google shows it", () => {
    const page = (results: unknown) => ({ results: [{ content: { results: { results } } }] });
    const news = decodoAnswer(page({ main: [{ title: "Story", url: "https://news.example/a", source: "Example", relative_publish_date: "2 hours ago" }] }), "news", 5);
    const videos = decodoAnswer(page({ organic: [], organic_videos: [{ title: "Clip", url: "https://video.example/v" }] }), "videos", 5);
    const images = decodoAnswer(page({ organic: [{ title: "Photo", link: "https://site.example/p", domain: "site.example", high_res_image: "https://img.example/p.jpg" }] }), "images", 5);
    expect(news.results).toEqual([{ title: "Story", url: "https://news.example/a", source: "Example", published: "2 hours ago" }]);
    expect(videos.results).toEqual([{ title: "Clip", url: "https://video.example/v" }]);
    expect(images.results).toEqual([{ title: "Photo", url: "https://site.example/p", source: "site.example", image: "https://img.example/p.jpg" }]);
  });

  it("fails in plain language and never repeats the provider's body", async () => {
    provider({ message: "upstream exploded with secrets" }, 502);
    setDeviceModels(models({ provider: "serper", apiKey: "k" }));
    const failed = await deviceWebSearch({ query: "anything that fails" });
    expect(failed).toEqual({ tool_error: { code: "web_search_failed", message: expect.stringContaining("did not work") } });
    provider({}, 403);
    expect(await deviceWebSearch({ query: "a refused key" })).toMatchObject({ tool_error: { code: "web_search_key_refused" } });
    expect(await deviceWebSearch({ query: "  " })).toMatchObject({ tool_error: { code: "invalid_arguments" } });
  });

  it("gives the timeline the query and the links found", () => {
    expect(deviceToolActivity({ query: "tea" }, { results: [{ title: "Tea", url: "https://tea.example", snippet: "long" }] })).toEqual({
      device_arguments: { query: "tea" }, device_result: { results: [{ title: "Tea", url: "https://tea.example" }] },
    });
  });
});
