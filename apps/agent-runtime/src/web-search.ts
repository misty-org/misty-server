import { generateText, isStepCount } from "ai";
import { z } from "zod";
import { ModelCallError } from "./model-calls.js";
import { instanceGateway } from "./model-provider.js";

// Misty's web search. The AI Gateway runs the search engine on Misty's own
// key, so nobody signs in to a search service. A small model only issues the
// one search call; its answer is never used, the engine's results are.

const engines = ["parallel", "perplexity", "exa"] as const;
type Engine = (typeof engines)[number];
type Recency = "day" | "week" | "month" | "year";

const domain = z.string().min(3).max(253).regex(/^[a-z0-9.-]+\.[a-z]{2,}$/i);

const searchRequest = z.object({
  model: z.string().min(3).max(200).regex(/^[^\s/]+\/\S+$/),
  engine: z.enum(engines),
  query: z.string().trim().min(1).max(2000),
  maxResults: z.number().int().min(1).max(10),
  domains: z.array(domain).max(10).optional(),
  recency: z.enum(["day", "week", "month", "year"]).optional(),
});

export interface WebSearchResult {
  title: string;
  url: string;
  snippet: string;
  published?: string;
}

const snippetLimit = 1500;
const recencyDays: Record<Recency, number> = { day: 1, week: 7, month: 31, year: 366 };

function afterDate(recency: Recency | undefined, now = Date.now()) {
  return recency ? new Date(now - recencyDays[recency] * 86_400_000).toISOString().slice(0, 10) : undefined;
}

function engineTool(request: z.infer<typeof searchRequest>) {
  const tools = instanceGateway().tools;
  const after = afterDate(request.recency);
  switch (request.engine) {
    case "perplexity":
      return tools.perplexitySearch({
        maxResults: request.maxResults,
        maxTokensPerPage: 1024,
        searchDomainFilter: request.domains,
        searchRecencyFilter: request.recency,
      });
    case "exa":
      return tools.exaSearch({
        numResults: request.maxResults,
        includeDomains: request.domains,
        startPublishedDate: after,
        contents: { highlights: { maxCharacters: snippetLimit } },
      });
    default:
      return tools.parallelSearch({
        mode: "agentic",
        maxResults: request.maxResults,
        sourcePolicy: { includeDomains: request.domains, afterDate: after },
        excerpts: { maxCharsPerResult: snippetLimit },
      });
  }
}

function text(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

/** Each engine's results in one shape the agent reads the same way. */
export function normalizeSearchOutput(engine: Engine, output: unknown, limit = 10): WebSearchResult[] {
  const body = (output ?? {}) as { error?: unknown; results?: unknown };
  if (body.error !== undefined || !Array.isArray(body.results)) {
    throw new ModelCallError(502, "web_search_failed", "The web search did not return results.");
  }
  const results: WebSearchResult[] = [];
  for (const raw of body.results as Array<Record<string, unknown>>) {
    const url = text(raw.url);
    if (!/^https?:\/\//i.test(url)) continue;
    const snippet =
      engine === "parallel" ? text(raw.excerpt)
      : engine === "perplexity" ? text(raw.snippet)
      : text(raw.summary) || (Array.isArray(raw.highlights) ? raw.highlights.map(text).join(" … ") : "") || text(raw.text);
    const published = text(engine === "parallel" ? raw.publishDate : engine === "perplexity" ? raw.date : raw.publishedDate);
    results.push({
      title: text(raw.title) || url,
      url,
      snippet: snippet.slice(0, snippetLimit),
      ...(published ? { published } : {}),
    });
    if (results.length >= limit) break;
  }
  return results;
}

export async function webSearchCall(body: unknown, signal?: AbortSignal) {
  const parsed = searchRequest.safeParse(body);
  if (!parsed.success) throw new ModelCallError(400, "invalid_model_call", "Invalid model call");
  const request = parsed.data;
  // Gateway tools run only through the Gateway, even for OpenAI models.
  const result = await generateText({
    model: instanceGateway()(request.model),
    system: "Call web_search exactly once with the user's query as written. Do not answer it yourself.",
    prompt: request.query,
    tools: { web_search: engineTool(request) },
    toolChoice: { type: "tool", toolName: "web_search" },
    stopWhen: isStepCount(1),
    maxOutputTokens: 400,
    maxRetries: 1,
    abortSignal: signal,
  });
  const part = result.content.find(
    (item) => (item.type === "tool-result" || item.type === "tool-error") && item.toolName === "web_search",
  );
  if (!part || part.type !== "tool-result") {
    throw new ModelCallError(502, "web_search_failed", "The web search did not return results.");
  }
  return {
    engine: request.engine,
    results: normalizeSearchOutput(request.engine, part.output, request.maxResults),
    usage: {
      inputTokens: result.usage.inputTokens ?? 0,
      cachedInputTokens: result.usage.inputTokenDetails?.cacheReadTokens ?? 0,
      outputTokens: result.usage.outputTokens ?? 0,
      reasoningTokens: result.usage.outputTokenDetails?.reasoningTokens ?? 0,
    },
  };
}
