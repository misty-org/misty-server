import { generateText, streamText, tool } from "ai";
import { afterEach, describe, expect, it, vi } from "vitest";
import { z } from "zod";
import { chatgptBody, streamToResponse } from "../src/chatgpt-provider.js";
import { setDeviceModels } from "../src/device-models.js";
import { RoutedModel } from "../src/model-calls.js";
import { resolveEmbeddingModel, type ModelRoute } from "../src/model-provider.js";

const token = "chatgpt-access-token-fixture";
const route: ModelRoute = {
  provider: "device",
  credential: { provider: "chatgpt", apiKey: token },
};

/** What the preview refuses (developers.openai.com/siwc/token-sharing-open-source/preview-limitations). */
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
];

const completed = (text: string) => ({
  id: "resp_1",
  object: "response",
  created_at: 1_790_000_000,
  model: "gpt-test",
  status: "completed",
  incomplete_details: null,
  output: [
    {
      type: "message",
      id: "msg_1",
      role: "assistant",
      status: "completed",
      content: [{ type: "output_text", text, annotations: [] }],
    },
  ],
  usage: {
    input_tokens: 5,
    output_tokens: 2,
    input_tokens_details: { cached_tokens: 0 },
    output_tokens_details: { reasoning_tokens: 0 },
  },
});

const sse = (...events: unknown[]) =>
  new Response(
    events
      .map(
        (event) => `event: ${(event as { type: string }).type}\ndata: ${JSON.stringify(event)}\n\n`,
      )
      .join(""),
    {
      status: 200,
      headers: { "content-type": "text/event-stream" },
    },
  );

afterEach(() => {
  vi.unstubAllGlobals();
  setDeviceModels(null);
});

describe("ChatGPT request shaping", () => {
  it("drops every refused field and moves system text into instructions", () => {
    const body = chatgptBody({
      model: "gpt-test",
      input: [
        { role: "system", content: "Be brief." },
        { role: "developer", content: "Developer note." },
        { role: "user", content: [{ type: "input_text", text: "hi" }] },
      ],
      instructions: "Misty.",
      ...Object.fromEntries(forbidden.map((field) => [field, 1])),
      store: true,
    });
    for (const field of forbidden) expect(body).not.toHaveProperty(field);
    expect(body.store).toBe(false);
    expect(body.stream).toBe(true);
    expect(body.instructions).toBe("Misty.\n\nBe brief.");
    expect(body.input).toEqual([
      { role: "developer", content: "Developer note." },
      { role: "user", content: [{ type: "input_text", text: "hi" }] },
    ]);
  });

  it("calls the Responses API with the token and only the accepted fields", async () => {
    const requests: Array<{ url: string; headers: Headers; body: Record<string, unknown> }> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit) => {
        requests.push({
          url: String(url),
          headers: new Headers(init.headers),
          body: JSON.parse(String(init.body)),
        });
        return sse(
          { type: "response.created", response: { id: "resp_1" } },
          { type: "response.completed", response: completed("Hello there") },
        );
      }),
    );
    const result = await generateText({
      model: new RoutedModel("chatgpt/gpt-test", route),
      system: "You are Misty.",
      prompt: "Say hello",
      maxOutputTokens: 500,
      temperature: 0.2,
      topP: 0.9,
      tools: { look: tool({ description: "Look.", inputSchema: z.object({}) }) },
      providerOptions: {
        openai: { user: "someone", metadata: { a: "b" }, previousResponseId: "resp_0" },
      },
    });
    expect(result.text).toBe("Hello there");
    expect(requests).toHaveLength(1);
    const [request] = requests;
    expect(request!.url).toBe("https://api.openai.com/v1/responses");
    expect(request!.headers.get("authorization")).toBe(`Bearer ${token}`);
    for (const field of forbidden) expect(request!.body).not.toHaveProperty(field);
    expect(request!.body).toMatchObject({ model: "gpt-test", store: false, stream: true });
    expect(String(request!.body.instructions)).toContain("You are Misty.");
    const roles = (request!.body.input as Array<{ role?: string }>).map((item) => item.role);
    expect(roles).not.toContain("system");
  });

  it("streams as asked, with the same shaping", async () => {
    let body: Record<string, unknown> = {};
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init: RequestInit) => {
        body = JSON.parse(String(init.body));
        return sse(
          {
            type: "response.created",
            response: { id: "resp_1", created_at: 1_790_000_000, model: "gpt-test" },
          },
          {
            type: "response.output_item.added",
            output_index: 0,
            item: {
              type: "message",
              id: "msg_1",
              role: "assistant",
              status: "in_progress",
              content: [],
            },
          },
          {
            type: "response.output_text.delta",
            item_id: "msg_1",
            output_index: 0,
            content_index: 0,
            delta: "Hi",
          },
          { type: "response.output_item.done", output_index: 0, item: completed("Hi").output[0] },
          { type: "response.completed", response: completed("Hi") },
        );
      }),
    );
    const result = streamText({
      model: new RoutedModel("chatgpt/gpt-test", route),
      system: "Be brief.",
      prompt: "Hi",
      maxOutputTokens: 100,
    });
    expect(await result.text).toBe("Hi");
    expect(body).toMatchObject({ store: false, stream: true, instructions: "Be brief." });
    expect(body).not.toHaveProperty("max_output_tokens");
  });

  it("reports a plan usage limit as a 429", async () => {
    const response = await streamToResponse(
      sse({
        type: "response.failed",
        response: {
          id: "resp_1",
          status: "failed",
          error: { code: "subscription_sharing_usage_limit_exceeded", message: "Limit reached" },
        },
      }),
    );
    expect(response.status).toBe(429);
    expect(await response.json()).toMatchObject({
      error: { code: "subscription_sharing_usage_limit_exceeded" },
    });
  });

  it("treats a stream that ends early as a failure", async () => {
    const response = await streamToResponse(
      sse({ type: "response.created", response: { id: "resp_1" } }),
    );
    expect(response.status).toBe(502);
  });

  it("has no embedding models", async () => {
    await expect(resolveEmbeddingModel("chatgpt/text-embedding-3-small", route)).rejects.toThrow(
      /model_unsupported/,
    );
  });

  it("takes a ChatGPT access token in the device's models", () => {
    expect(
      setDeviceModels({
        version: 1,
        providers: { chatgpt: { apiKey: token } },
        models: { agent: "chatgpt/gpt-test" },
      }),
    ).toBe(true);
  });
});
