import { clip, compactHit, field, googleRecency, items, number, postJSON, siteQuery, webLink, type Item, type WebSearchAnswer, type WebSearchHit, type WebSearchProvider, type WebSearchRequest } from "./provider.js";

// Decodo's Web Scraping API returns Google's results as parsed JSON, with the key as a
// bearer token. Every kind is the google_search target, with google_tbm picking the tab.
export const decodoEndpoint = { url: "https://data.decodo.com/v1/scrape" };

const tabs: Record<string, string> = { web: "", news: "nws", videos: "vid", images: "isch" };

// Where each kind's results sit in the parsed page (seen live 2026-11-01): news under main,
// videos under organic_videos.
const lists: Record<string, string[]> = {
  web: ["organic"],
  news: ["main", "organic"],
  videos: ["organic_videos", "organic"],
  images: ["organic"],
};

export function decodoBody(request: WebSearchRequest): Item {
  const body: Item = { target: "google_search", query: siteQuery(request.query, request.domains), parse: true, page_count: 1 };
  const tab = tabs[request.kind];
  if (tab) body.google_tbm = tab;
  const tbs = googleRecency[request.recency ?? ""];
  if (tbs) body.google_tbs = tbs;
  if (request.location?.trim()) body.geo = request.location.trim();
  return body;
}

function decodoHit(item: Item): WebSearchHit | undefined {
  const link = field(item, "url", "link");
  if (!webLink(link)) return undefined;
  return compactHit({
    title: field(item, "title") || link, url: link, snippet: field(item, "desc", "description", "snippet"),
    published: field(item, "relative_publish_date", "date"), source: field(item, "source", "domain"),
    channel: field(item, "channel", "author"), duration: field(item, "duration"),
    image: field(item, "high_res_image", "image"), rating: number(item, "rating"),
  });
}

/** Reads results[0].content.results.results: the first list that has results, and for a
 * web search Google's AI overview as the direct answer. */
export function decodoAnswer(body: unknown, kind: string, limit: number): WebSearchAnswer {
  const answer: WebSearchAnswer = { results: [] };
  const first = items((body as Item | null)?.results)[0];
  const page = (((first?.content as Item | undefined)?.results as Item | undefined)?.results ?? {}) as Item;
  for (const list of lists[kind] ?? lists.web!) {
    const found = items(page[list]);
    if (!found.length) continue;
    for (const item of found) {
      const hit = decodoHit(item);
      if (hit) answer.results.push(hit);
      if (answer.results.length >= limit) break;
    }
    break;
  }
  const overview = kind === "web" ? items(page.ai_overviews)[0] : undefined;
  const text = overview ? field(overview, "answer_text") : "";
  if (text) answer.answer = clip(text);
  return answer;
}

export const decodoProvider: WebSearchProvider = {
  name: "decodo",
  kinds: Object.keys(tabs),
  async search(key, request, signal) {
    const body = await postJSON("decodo", decodoEndpoint.url, { Authorization: `Bearer ${key}`, Accept: "application/json" }, decodoBody(request), signal);
    return decodoAnswer(body, request.kind, request.limit);
  },
};
