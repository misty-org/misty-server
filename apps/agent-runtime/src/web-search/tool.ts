import type { MCPRemoteTool, SpaceTaskContext } from "../types.js";
import { decodoProvider } from "./decodo.js";
import type { SearchProviderId, WebSearchProvider } from "./provider.js";
import { serperProvider } from "./serper.js";

/** The tool's name, the same as when it ran on the server. */
export const webSearchTool = "web.search";

export const searchProviders: Record<SearchProviderId, WebSearchProvider> = { decodo: decodoProvider, serper: serperProvider };

// How each kind reads to the model; the tool lists only the kinds the device's provider offers.
const kindPhrases: Record<string, string> = {
  web: "web (default)",
  news: "news",
  videos: "videos (YouTube and others, with channel and length)",
  images: "images",
  places: "places (businesses with address, rating and phone)",
  shopping: "shopping (products with price)",
  scholar: "scholar (papers with citations)",
  patents: "patents",
  maps: "maps (places around a point, set near for coordinates)",
  autocomplete: "autocomplete (what people commonly search for a phrase, without links; useful to find the right words)",
};
const kindOrder = Object.keys(kindPhrases);

function phrases(provider: WebSearchProvider): string {
  const listed = kindOrder.filter((kind) => provider.kinds.includes(kind)).map((kind) => kindPhrases[kind]!);
  return listed.length < 2 ? listed.join("") : `${listed.slice(0, -1).join(", ")} or ${listed.at(-1)}`;
}

/** web.search as the model sees it, for the device's provider. */
export function webSearchDescriptor(id: SearchProviderId): MCPRemoteTool {
  const provider = searchProviders[id];
  return {
    name: webSearchTool,
    description:
      "Search the web for current or outside information: research, news, prices, documentation, or facts you are unsure of. " +
      "Write the query as keywords someone would put in a title or page, not as a question to you. " +
      `kind picks Google's tab: ${phrases(provider)}. ` +
      "Returns titles, links and short snippets; read a page with web.fetch when the snippets are not enough, and cite the links you use. " +
      "Runs on this device's own search key, so it needs no app or sign-in. Use it for web research instead of search apps.",
    inputSchema: {
      type: "object", required: ["query"], additionalProperties: false,
      properties: {
        query: { type: "string", minLength: 1, maxLength: 400, description: "What to find, as search keywords" },
        kind: { type: "string", enum: [...provider.kinds].sort(), description: "Which kind of results; web when unsure" },
        domains: { type: "array", maxItems: 10, description: "Only search these sites, such as nih.gov or youtube.com", items: { type: "string", pattern: "^[A-Za-z0-9.-]+\\.[A-Za-z]{2,}$" } },
        recency: { type: "string", enum: ["day", "week", "month", "year"], description: "Only results from this recent period" },
        location: { type: "string", maxLength: 100, description: "Search as if from this place, such as Austin, Texas; for places, news and shopping" },
        near: { type: "string", pattern: "^@-?\\d{1,3}(\\.\\d+)?,-?\\d{1,3}(\\.\\d+)?,\\d{1,2}(\\.\\d+)?z$", description: "For maps: center as @latitude,longitude,zoomz, such as @30.27,-97.74,14z" },
        max_results: { type: "integer", minimum: 1, maximum: 10 },
      },
    },
    readOnly: true,
    // It holds no durable wait, so it may run alongside other reads.
    concurrent: true,
    local: true,
  };
}

/** The run's catalog with the tools this device runs itself: web.search, when the device has a
 * search key. Without one the tool isn't offered, and the server's prompt doesn't name it. */
export function withDeviceTools(descriptors: MCPRemoteTool[], context: Pick<SpaceTaskContext, "device_search">): MCPRemoteTool[] {
  const others = descriptors.filter((descriptor) => descriptor.name !== webSearchTool);
  return context.device_search ? [webSearchDescriptor(context.device_search), ...others] : others;
}

/** What the conversation's timeline shows about a device call: its query and the links found.
 * The server never sees the call itself, so the finished checkpoint carries this much. */
export function deviceToolActivity(input: unknown, output: unknown): Record<string, unknown> {
  const query = (input as { query?: unknown } | null)?.query;
  const results = (output as { results?: unknown } | null)?.results;
  const rows = Array.isArray(results)
    ? results.slice(0, 10).map((hit) => ({ title: String((hit as { title?: unknown })?.title ?? "").slice(0, 200), url: String((hit as { url?: unknown })?.url ?? "").slice(0, 2000) }))
    : [];
  return {
    device_arguments: { query: typeof query === "string" ? query.slice(0, 400) : "" },
    device_result: { results: rows },
  };
}
