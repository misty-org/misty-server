import { afterEach, describe, expect, it, vi } from "vitest";
import { WORKFLOW_DESERIALIZE, WORKFLOW_SERIALIZE } from "@workflow/serde";
import { InstanceModel } from "../src/instance-model.js";
import { instanceModelConfig, resolveInstanceModel } from "../src/model-provider.js";
import type { LanguageModelV4CallOptions } from "@ai-sdk/provider";
import { WorkflowAgent } from "@ai-sdk/workflow";
import { isStepCount, tool } from "ai";
import { z } from "zod";

const options: LanguageModelV4CallOptions = {
  prompt: [{ role: "user", content: [{ type: "text", text: "hello" }] }],
  providerOptions: { gateway: { models: ["other/provider"] } },
  reasoning: "high",
};
afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

describe("instance model routing", () => {
  it("preserves the existing gateway path and fallback options by default", () => {
    const result = resolveInstanceModel("openai/example", options, {
      AI_GATEWAY_API_KEY: "fixture",
    });
    expect(result.model.provider).toBe("gateway");
    expect(result.options).toBe(options);
  });

  it.each(["openai", "anthropic", "google", "openai-compatible"])(
    "routes %s directly without gateway fallback",
    (provider) => {
      const result = resolveInstanceModel(`${provider}/example`, options, {
        MISTY_AGENT_MODEL_PROVIDER: provider,
        MISTY_AGENT_MODEL: `${provider}/example`,
        MISTY_AGENT_MODEL_API_KEY: "fixture-key",
        MISTY_AGENT_MODEL_BASE_URL: "https://provider.example/v1",
      });
      expect(result.model.modelId).toBe("example");
      expect(result.model.provider).not.toBe("gateway");
      expect(result.options.providerOptions).not.toHaveProperty("gateway");
    },
  );

  it("rejects incomplete configuration, hosted overrides and unconfigured model IDs", () => {
    for (const env of [
      { MISTY_AGENT_MODEL_PROVIDER: "unknown" },
      { MISTY_AGENT_MODEL_PROVIDER: "openai" },
      {
        MISTY_AGENT_MODEL_PROVIDER: "openai-compatible",
        MISTY_AGENT_MODEL: "openai-compatible/local",
      },
      {
        MISTY_AGENT_MODEL_PROVIDER: "openai",
        MISTY_AGENT_MODEL: "anthropic/example",
        MISTY_AGENT_MODEL_API_KEY: "secret",
      },
      { MISTY_AGENT_MODEL_API_KEY: "secret" },
      {
        MISTY_AGENT_MODEL_PROVIDER: "openai-compatible",
        MISTY_AGENT_MODEL: "openai-compatible/local",
        MISTY_AGENT_MODEL_BASE_URL: "https://user:secret@example.com/v1",
      },
    ])
      expect(() => instanceModelConfig(env)).toThrow();
    expect(() =>
      resolveInstanceModel("anthropic/wrong", options, {
        MISTY_AGENT_MODEL_PROVIDER: "anthropic",
        MISTY_AGENT_MODEL: "anthropic/example",
        MISTY_AGENT_MODEL_API_KEY: "secret",
      }),
    ).toThrow("not configured");
  });

  it("keeps credentials out of durable state and uses the current key at execution", async () => {
    vi.stubEnv("MISTY_AGENT_MODEL_PROVIDER", "openai-compatible");
    vi.stubEnv("MISTY_AGENT_MODEL", "openai-compatible/local/example");
    vi.stubEnv("MISTY_AGENT_MODEL_API_KEY", "old-secret");
    vi.stubEnv("MISTY_AGENT_MODEL_BASE_URL", "http://localhost:1234/v1");
    const serialized = InstanceModel[WORKFLOW_SERIALIZE](
      new InstanceModel("openai-compatible/local/example"),
    );
    expect(serialized).toEqual({ modelId: "openai-compatible/local/example" });
    vi.stubEnv("MISTY_AGENT_MODEL_API_KEY", "rotated-secret");
    const fetch = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            id: "response",
            created: 1,
            model: "local/example",
            choices: [
              {
                index: 0,
                message: { role: "assistant", content: "hello back" },
                finish_reason: "stop",
              },
            ],
            usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
          }),
          { headers: { "Content-Type": "application/json" } },
        ),
    );
    vi.stubGlobal("fetch", fetch);
    const model = InstanceModel[WORKFLOW_DESERIALIZE](serialized);
    const result = await model.doGenerate(options);
    expect(result.content).toContainEqual({ type: "text", text: "hello back" });
    const [url, request] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("http://localhost:1234/v1/chat/completions");
    expect(new Headers(request.headers).get("authorization")).toBe("Bearer rotated-secret");
    const body = JSON.parse(request.body as string);
    expect(body.model).toBe("local/example");
    expect(body).not.toHaveProperty("reasoning_effort");
    expect(body).not.toHaveProperty("gateway");
  });

  it("supports a keyless local OpenAI-compatible endpoint", () => {
    expect(
      instanceModelConfig({
        MISTY_AGENT_MODEL_PROVIDER: "openai-compatible",
        MISTY_AGENT_MODEL: "openai-compatible/local",
        MISTY_AGENT_MODEL_BASE_URL: "http://host.docker.internal:11434/v1",
      }).apiKey,
    ).toBeUndefined();
  });

  it.each([
    ["openai", "/responses", "authorization", "Bearer fixture-key"],
    ["anthropic", "/messages", "x-api-key", "fixture-key"],
    ["google", "/models/example:generateContent", "x-goog-api-key", "fixture-key"],
  ])(
    "sends %s credentials only to its configured provider endpoint",
    async (provider, path, header, expected) => {
      const fetch = vi.fn(
        async () =>
          new Response('{"error":{"message":"fixture rejection"}}', {
            status: 401,
            headers: { "content-type": "application/json" },
          }),
      );
      vi.stubGlobal("fetch", fetch);
      const result = resolveInstanceModel(`${provider}/example`, options, {
        MISTY_AGENT_MODEL_PROVIDER: provider,
        MISTY_AGENT_MODEL: `${provider}/example`,
        MISTY_AGENT_MODEL_API_KEY: "fixture-key",
        MISTY_AGENT_MODEL_BASE_URL: "https://provider.example/v1",
      });
      await expect(result.model.doGenerate(result.options)).rejects.toThrow();
      const [url, request] = fetch.mock.calls[0] as unknown as [string, RequestInit];
      expect(url).toBe(`https://provider.example/v1${path}`);
      expect(new Headers(request.headers).get(header!)).toBe(expected);
      expect(fetch).toHaveBeenCalledTimes(1);
    },
  );

  it("streams a tool call and final answer through the real WorkflowAgent adapter", async () => {
    vi.stubEnv("MISTY_AGENT_MODEL_PROVIDER", "openai-compatible");
    vi.stubEnv("MISTY_AGENT_MODEL", "openai-compatible/local");
    vi.stubEnv("MISTY_AGENT_MODEL_BASE_URL", "http://localhost:1234/v1");
    vi.stubEnv("MISTY_AGENT_MODEL_API_KEY", "");
    let calls = 0;
    const fetch = vi.fn(async () => {
      const delta =
        calls++ === 0
          ? {
              tool_calls: [
                {
                  index: 0,
                  id: "read-1",
                  type: "function",
                  function: { name: "read_note", arguments: "{}" },
                },
              ],
            }
          : { content: "The note says hello." };
      const chunks = [
        {
          id: "result",
          created: 1,
          model: "local",
          choices: [{ index: 0, delta, finish_reason: null }],
        },
        {
          id: "result",
          created: 1,
          model: "local",
          choices: [{ index: 0, delta: {}, finish_reason: calls === 1 ? "tool_calls" : "stop" }],
          usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 },
        },
      ];
      return new Response(
        chunks.map((chunk) => `data: ${JSON.stringify(chunk)}\n\n`).join("") + "data: [DONE]\n\n",
        {
          headers: { "content-type": "text/event-stream" },
        },
      );
    });
    vi.stubGlobal("fetch", fetch);
    const read = vi.fn(async () => ({ text: "hello" }));
    const agent = new WorkflowAgent({
      model: new InstanceModel("openai-compatible/local"),
      stopWhen: isStepCount(2),
      tools: { read_note: tool({ inputSchema: z.object({}), execute: read }) },
    });
    const result = await agent.stream({
      prompt: "Read the note",
      providerOptions: options.providerOptions,
    });
    expect(read).toHaveBeenCalledOnce();
    expect(result.steps.at(-1)?.text).toBe("The note says hello.");
    expect(result.steps.at(-1)?.usage.totalTokens).toBeGreaterThan(0);
    expect(fetch).toHaveBeenCalledTimes(2);
    for (const call of fetch.mock.calls) {
      const [, request] = call as unknown as [string, RequestInit];
      expect(new Headers(request.headers).has("authorization")).toBe(false);
      expect(JSON.parse(request.body as string)).not.toHaveProperty("gateway");
    }
  });
});
