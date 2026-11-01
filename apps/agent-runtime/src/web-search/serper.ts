import { clip, compactHit, field, googleRecency, items, number, postJSON, siteQuery, webLink, type Item, type WebSearchAnswer, type WebSearchHit, type WebSearchProvider } from "./provider.js";

// Serper (serper.dev) sells Google's result tabs as JSON, one endpoint per tab, with the
// key in X-API-KEY. Each kind is one request.
export const serperBase = { url: "https://google.serper.dev" };

const kinds: Record<string, { path: string; list: string }> = {
  web: { path: "/search", list: "organic" },
  news: { path: "/news", list: "news" },
  videos: { path: "/videos", list: "videos" },
  images: { path: "/images", list: "images" },
  places: { path: "/places", list: "places" },
  shopping: { path: "/shopping", list: "shopping" },
  scholar: { path: "/scholar", list: "organic" },
  patents: { path: "/patents", list: "organic" },
  maps: { path: "/maps", list: "places" },
  // Autocomplete returns what people type, not pages: suggestions without links.
  autocomplete: { path: "/autocomplete", list: "suggestions" },
};

/** The request body: site: filters join the query, recency is tbs, and a map search can
 * center on coordinates ("@lat,lng,zoomz"). */
export function serperQuery(request: { query: string; domains: string[]; recency?: string; location?: string; near?: string; limit: number }): Item {
  const body: Item = { q: siteQuery(request.query, request.domains), num: request.limit };
  const tbs = googleRecency[request.recency ?? ""];
  if (tbs) body.tbs = tbs;
  if (request.location?.trim()) body.location = request.location.trim();
  if (request.near?.trim()) body.ll = request.near.trim();
  return body;
}

/** One result of any kind; a place without a website links to its map entry. */
function serperHit(item: Item): WebSearchHit | undefined {
  let link = field(item, "link", "website");
  if (!link && field(item, "cid")) link = `https://maps.google.com/?cid=${encodeURIComponent(field(item, "cid"))}`;
  if (!webLink(link)) return undefined;
  return compactHit({
    title: field(item, "title") || link, url: link, snippet: field(item, "snippet", "description"),
    published: field(item, "date", "year", "publicationDate", "priorityDate"),
    source: field(item, "source"), channel: field(item, "channel"), duration: field(item, "duration"), price: field(item, "price"),
    rating: number(item, "rating"), reviews: Math.trunc(number(item, "ratingCount")), address: field(item, "address"),
    phone: field(item, "phoneNumber"), category: field(item, "category", "type"), image: field(item, "imageUrl"),
    publication: field(item, "publicationInfo"), cited_by: Math.trunc(number(item, "citedBy")),
  });
}

/** Reads one kind's result list plus any direct answer. */
export function serperAnswer(body: unknown, list: string, limit: number): WebSearchAnswer {
  const page = (body && typeof body === "object" ? body : {}) as Item;
  const answer: WebSearchAnswer = { results: [] };
  for (const item of items(page[list])) {
    const suggestion = list === "suggestions" ? field(item, "value") : "";
    const hit = suggestion ? { title: suggestion } : serperHit(item);
    if (hit) answer.results.push(hit);
    if (answer.results.length >= limit) break;
  }
  const box = (page.answerBox ?? {}) as Item;
  const graph = (page.knowledgeGraph ?? {}) as Item;
  const direct = [field(box, "answer"), field(box, "snippet"), field(graph, "description")].find(Boolean);
  if (direct) answer.answer = clip(direct);
  return answer;
}

export const serperProvider: WebSearchProvider = {
  name: "serper",
  kinds: Object.keys(kinds),
  async search(key, request, signal) {
    const endpoint = kinds[request.kind] ?? kinds.web!;
    const body = await postJSON("serper", serperBase.url + endpoint.path, { "X-API-KEY": key }, serperQuery(request), signal);
    return serperAnswer(body, endpoint.list, request.limit);
  },
};
