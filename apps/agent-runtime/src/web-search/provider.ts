/**
 * web.search runs here, on the person's device, on the search key they keep in Settings ›
 * Models (docs/agents/runtime.md). One provider is chosen per device; every provider takes
 * the same request and answers with the same hits, and says which kinds of search it offers.
 * The Misty server holds no search key, makes no search call and counts no searches.
 */

export const searchProviderIds = ["decodo", "serper"] as const;
export type SearchProviderId = (typeof searchProviderIds)[number];

/** One search, the same for every provider. */
export interface WebSearchRequest {
  query: string;
  kind: string;
  domains: string[];
  recency?: string;
  location?: string;
  near?: string;
  limit: number;
}

/** One result; fields past the snippet appear only for the kinds that have them. */
export interface WebSearchHit {
  title: string;
  url?: string;
  snippet?: string;
  published?: string;
  source?: string;
  channel?: string;
  duration?: string;
  price?: string;
  rating?: number;
  reviews?: number;
  address?: string;
  phone?: string;
  category?: string;
  image?: string;
  publication?: string;
  cited_by?: number;
}

export interface WebSearchAnswer {
  results: WebSearchHit[];
  /** Google's direct answer or overview text, when there is one. */
  answer?: string;
}

export interface WebSearchProvider {
  name: SearchProviderId;
  /** The kinds this provider offers, web first. */
  kinds: readonly string[];
  search(key: string, request: WebSearchRequest, signal: AbortSignal): Promise<WebSearchAnswer>;
}

/** A provider call that failed; its message never holds the provider's body or the key. */
export class WebSearchFailed extends Error {
  constructor(readonly provider: string, readonly status: number) {
    super(`${provider} search failed (${status || "network"})`);
    this.name = "WebSearchFailed";
  }
}

const maxBody = 1 << 20;

/** POSTs JSON and reads at most 1 MiB of JSON back. */
export async function postJSON(provider: string, url: string, headers: Record<string, string>, body: unknown, signal: AbortSignal): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json", ...headers }, body: JSON.stringify(body), signal });
  } catch {
    throw new WebSearchFailed(provider, 0);
  }
  if (response.status !== 200) {
    await response.body?.cancel().catch(() => undefined);
    throw new WebSearchFailed(provider, response.status);
  }
  const text = await response.text();
  if (text.length > maxBody) throw new WebSearchFailed(provider, response.status);
  try {
    return JSON.parse(text) as unknown;
  } catch {
    throw new WebSearchFailed(provider, response.status);
  }
}

/** Adds the site: filters every Google-backed provider understands. */
export function siteQuery(query: string, domains: string[]): string {
  const sites = domains.map((domain) => domain.trim()).filter(Boolean).map((domain) => `site:${domain}`);
  return sites.length ? `${query} (${sites.join(" OR ")})` : query;
}

/** Google's tbs value for a recency. */
export const googleRecency: Record<string, string> = { day: "qdr:d", week: "qdr:w", month: "qdr:m", year: "qdr:y" };

export type Item = Record<string, unknown>;

/** The first named field that holds text (or a number, written out). */
export function field(item: Item, ...names: string[]): string {
  for (const name of names) {
    const value = item[name];
    if (typeof value === "string" && value.trim()) return value.trim();
    if (typeof value === "number" && Number.isFinite(value)) return String(value);
  }
  return "";
}

export function number(item: Item, name: string): number {
  const value = item[name];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

export function items(value: unknown): Item[] {
  return Array.isArray(value) ? value.filter((entry): entry is Item => Boolean(entry) && typeof entry === "object" && !Array.isArray(entry)) : [];
}

export const webLink = (link: string) => link.startsWith("https://") || link.startsWith("http://");

/** Keeps a hit's filled fields only, as the tool has always answered. */
export function compactHit(hit: WebSearchHit): WebSearchHit {
  const kept = Object.entries(hit).filter(([, value]) => value !== "" && value !== 0 && value !== undefined);
  return Object.fromEntries(kept) as unknown as WebSearchHit;
}

/** A direct answer is at most 600 characters. */
export function clip(text: string, limit = 600): string {
  const chars = [...text];
  return chars.length <= limit ? text : `${chars.slice(0, limit - 1).join("").trim()}…`;
}
