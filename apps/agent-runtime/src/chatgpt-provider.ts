import { createOpenAI } from "@ai-sdk/openai";
import type { LanguageModelV4, LanguageModelV4CallOptions } from "@ai-sdk/provider";

/**
 * Sign in with ChatGPT's plan usage for open-source apps
 * (developers.openai.com/siwc/token-sharing-open-source). The desktop signs in and refreshes the
 * tokens; the runtime gets only a short-lived access token in the device's model document and
 * calls the Responses API with it as the bearer. Language models only.
 *
 * The preview accepts a narrow Responses request: `store: false` and `stream: true` on every
 * call, no system-role input items (their text goes into `instructions`), no
 * `previous_response_id`, and none of the fields below. Every request is reshaped here, at the
 * fetch, so nothing the SDK or a caller adds can slip through.
 */
export const chatgptBaseURL = "https://api.openai.com/v1";

const forbidden = [
  "background",
  "conversation",
  "max_output_tokens",
  "max_tool_calls",
  "metadata",
  "moderation",
  "multi_agent",
  "prompt",
  "prompt_cache_retention",
  "safety_identifier",
  "temperature",
  "top_logprobs",
  "top_p",
  "truncation",
  "user",
  "previous_response_id",
  "service_tier",
] as const;

type Item = { type?: string; role?: string; content?: unknown };

function itemText(content: unknown): string {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content
    .flatMap((part: { type?: string; text?: unknown }) =>
      typeof part?.text === "string" ? [part.text] : [],
    )
    .join("\n");
}

/** The body the preview accepts, from the body the SDK built. */
export function chatgptBody(body: Record<string, unknown>): Record<string, unknown> {
  const shaped: Record<string, unknown> = { ...body };
  for (const field of forbidden) delete shaped[field];
  const input = Array.isArray(body.input) ? (body.input as Item[]) : [];
  const system: string[] = [];
  const kept = input.filter((item) => {
    const isSystem =
      item?.role === "system" && (item.type === undefined || item.type === "message");
    if (isSystem) {
      const text = itemText(item.content).trim();
      if (text) system.push(text);
    }
    return !isSystem;
  });
  const instructions = [
    typeof body.instructions === "string" ? body.instructions.trim() : "",
    ...system,
  ]
    .filter(Boolean)
    .join("\n\n");
  shaped.input = kept;
  if (instructions) shaped.instructions = instructions;
  else delete shaped.instructions;
  shaped.store = false;
  shaped.stream = true;
  return shaped;
}

type StreamEvent = {
  type?: string;
  response?: Record<string, unknown> & { error?: { code?: string; message?: string } };
};

/** The HTTP status a failed plan-usage response maps to (Errors and recovery). */
function failureStatus(code: string | undefined): number {
  switch (code) {
    case "subscription_sharing_usage_limit_exceeded":
      return 429;
    case "subscription_sharing_usage_unavailable":
    case "subscription_sharing_user_unavailable":
      return 503;
    case "subscription_sharing_invalid_user":
      return 401;
    case "subscription_sharing_user_not_eligible":
    case "subscription_sharing_route_not_supported":
      return 403;
    default:
      return 400;
  }
}

/** Reads a whole event stream and answers as the non-streaming endpoint would. */
export async function streamToResponse(stream: Response): Promise<Response> {
  const text = await stream.text();
  let final: StreamEvent | undefined;
  for (const block of text.split(/\r?\n\r?\n/)) {
    const data = block
      .split(/\r?\n/)
      .filter((line) => line.startsWith("data:"))
      .map((line) => line.slice(5).trimStart())
      .join("\n");
    if (!data || data === "[DONE]") continue;
    let event: StreamEvent;
    try {
      event = JSON.parse(data) as StreamEvent;
    } catch {
      continue;
    }
    if (
      event.type === "response.completed" ||
      event.type === "response.incomplete" ||
      event.type === "response.failed"
    )
      final = event;
  }
  const json = (status: number, value: unknown) =>
    new Response(JSON.stringify(value), {
      status,
      headers: { "content-type": "application/json" },
    });
  if (!final?.response)
    return json(502, {
      error: {
        code: "stream_incomplete",
        message: "ChatGPT ended the answer before it was complete.",
      },
    });
  if (final.type === "response.failed") {
    const error = final.response.error ?? {};
    return json(failureStatus(error.code), {
      error: {
        code: error.code ?? "response_failed",
        message: error.message ?? "ChatGPT could not answer.",
      },
    });
  }
  return json(200, final.response);
}

/** fetch for ChatGPT plan usage: reshapes every request, and streams even when the caller didn't ask. */
export function chatgptFetch(base: typeof fetch = fetch): typeof fetch {
  return async (input, init) => {
    if (typeof init?.body !== "string") return base(input, init);
    let body: Record<string, unknown>;
    try {
      body = JSON.parse(init.body) as Record<string, unknown>;
    } catch {
      return base(input, init);
    }
    const streaming = body.stream === true;
    const headers = new Headers(init.headers);
    headers.set("accept", "text/event-stream");
    const response = await base(input, {
      ...init,
      headers,
      body: JSON.stringify(chatgptBody(body)),
    });
    if (streaming || !response.ok) return response;
    return streamToResponse(response);
  };
}

/** One ChatGPT language model on the device's access token. */
export function chatgptLanguageModel(
  accessToken: string,
  model: string,
  options: LanguageModelV4CallOptions,
  base: typeof fetch = fetch,
): { model: LanguageModelV4; options: LanguageModelV4CallOptions } {
  const { gateway: _gateway, ...providerOptions } = options.providerOptions ?? {};
  const openai = (providerOptions.openai ?? {}) as Record<string, unknown>;
  const provider = createOpenAI({
    apiKey: accessToken,
    baseURL: chatgptBaseURL,
    fetch: chatgptFetch(base),
  });
  return {
    model: provider.responses(model),
    options: {
      ...options,
      maxOutputTokens: undefined,
      temperature: undefined,
      topP: undefined,
      providerOptions: { ...providerOptions, openai: { ...openai, store: false } },
      tools: options.tools?.map((tool) =>
        tool.type === "function" && tool.strict == null ? { ...tool, strict: false } : tool,
      ),
    },
  };
}
