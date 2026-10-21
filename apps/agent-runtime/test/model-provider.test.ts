import { afterEach, describe, expect, it, vi } from "vitest";
import { WORKFLOW_DESERIALIZE, WORKFLOW_SERIALIZE } from "@workflow/serde";
import { InstanceModel } from "../src/instance-model.js";
import {
  imagesOutOfToolResults,
  instanceModelConfig,
  resolveEmbeddingModel,
  resolveInstanceModel,
  resolveTranscriptionModel,
} from "../src/model-provider.js";
import type { LanguageModelV4CallOptions } from "@ai-sdk/provider";

const options: LanguageModelV4CallOptions = {
  prompt: [{ role: "user", content: [{ type: "text", text: "hello" }] }],
  providerOptions: { gateway: { models: ["other/provider"] } },
  reasoning: "high",
};
const withKey = { AI_GATEWAY_API_KEY: "gateway-fixture", OPENAI_API_KEY: "openai-fixture" };

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

describe("model routing", () => {
  it("routes models through the AI Gateway without an OpenAI key", () => {
    const result = resolveInstanceModel("openai/example", options, {
      AI_GATEWAY_API_KEY: "fixture",
    });
    expect(result.model.provider).toBe("gateway");
    expect(result.options).toBe(options);
  });

  it("keeps non-OpenAI models on the Gateway even with an OpenAI key", () => {
    const result = resolveInstanceModel("anthropic/example", options, withKey);
    expect(result.model.provider).toBe("gateway");
    expect(result.options).toBe(options);
  });

  it("sends OpenAI models to OpenAI with the instance key", async () => {
    const fetch = rejectingFetch();
    vi.stubGlobal("fetch", fetch);
    const resolved = resolveInstanceModel("openai/gpt-6-luna", options, withKey);
    expect(resolved.model.provider).not.toBe("gateway");
    expect(resolved.options.providerOptions).not.toHaveProperty("gateway");
    await expect(resolved.model.doGenerate(resolved.options)).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(1);
    const [url, request] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("https://api.openai.com/v1/responses");
    expect(new Headers(request.headers).get("authorization")).toBe("Bearer openai-fixture");
    const body = JSON.parse(request.body as string);
    expect(body.model).toBe("gpt-6-luna");
    expect(body.reasoning.effort).toBe("high");
  });

  it("keeps optional browser arguments optional on the Responses wire", async () => {
    const fetch = rejectingFetch();
    vi.stubGlobal("fetch", fetch);
    const toolOptions: LanguageModelV4CallOptions = {
      ...options,
      tools: [
        {
          type: "function",
          name: "browser_inspect",
          inputSchema: {
            type: "object",
            properties: {
              scopeId: { type: "string" },
              extensionId: { type: "integer", minimum: 1 },
            },
            required: ["scopeId"],
            additionalProperties: false,
          },
        },
        {
          type: "function",
          name: "explicit_strict",
          strict: true,
          inputSchema: {
            type: "object",
            properties: {},
            required: [],
            additionalProperties: false,
          },
        },
      ],
    };
    const resolved = resolveInstanceModel("openai/gpt-6-luna", toolOptions, withKey);
    await expect(resolved.model.doGenerate(resolved.options)).rejects.toThrow();
    const [, request] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    const body = JSON.parse(request.body as string);
    expect(body.tools[0].strict).toBe(false);
    expect(body.tools[0].parameters.required).toEqual(["scopeId"]);
    expect(body.tools[1].strict).toBe(true);
    expect(toolOptions.tools?.[0]).not.toHaveProperty("strict");
  });

  it("applies the same rule to embeddings and transcription", async () => {
    expect((await resolveEmbeddingModel("openai/text-embedding-3-small", withKey)).provider).not.toBe(
      "gateway",
    );
    expect((await resolveEmbeddingModel("google/gemini-embedding", withKey)).provider).toBe("gateway");
    expect((await resolveEmbeddingModel("openai/text-embedding-3-small", {})).provider).toBe("gateway");
    expect((await resolveTranscriptionModel("openai/whisper-1", withKey)).provider).not.toBe("gateway");
    expect((await resolveTranscriptionModel("openai/whisper-1", {})).provider).toBe("gateway");
  });

  it("rejects invalid instance models", () => {
    for (const model of ["example", "openai/", "openai/has space"])
      expect(() => instanceModelConfig({ MISTY_AGENT_MODEL: model })).toThrow();
    expect(instanceModelConfig({})).toEqual({ provider: "gateway", model: "" });
  });

  it("keeps credentials out of durable model state", () => {
    const serialized = InstanceModel[WORKFLOW_SERIALIZE](new InstanceModel("openai/example"));
    expect(serialized).toEqual({ modelId: "openai/example", identity: undefined, role: "agent" });
    expect(InstanceModel[WORKFLOW_DESERIALIZE](serialized).modelId).toBe("openai/example");
  });
});

function rejectingFetch() {
  return vi.fn<typeof globalThis.fetch>(
    async () =>
      new Response('{"error":{"message":"fixture rejection"}}', {
        status: 401,
        headers: { "content-type": "application/json" },
      }),
  );
}

describe("direct Anthropic", () => {
  const anthropicKey = { AI_GATEWAY_API_KEY: "gateway-fixture", CLAUDE_API_KEY: "anthropic-fixture" };

  it("sends Anthropic models to Anthropic with the instance key, in Anthropic's own naming", async () => {
    const fetch = rejectingFetch();
    vi.stubGlobal("fetch", fetch);
    const resolved = resolveInstanceModel("anthropic/claude-haiku-5.5", options, anthropicKey);
    expect(resolved.model.provider).not.toBe("gateway");
    expect(resolved.options.providerOptions).not.toHaveProperty("gateway");
    await expect(resolved.model.doGenerate(resolved.options)).rejects.toThrow();
    const [url, request] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("https://api.anthropic.com/v1/messages");
    expect(new Headers(request.headers).get("x-api-key")).toBe("anthropic-fixture");
    expect(JSON.parse(request.body as string).model).toBe("claude-haiku-5-5");
  });

  it("keeps Anthropic models on the Gateway without a key, and other models off Anthropic", () => {
    expect(resolveInstanceModel("anthropic/claude-haiku-5.5", options, { AI_GATEWAY_API_KEY: "x" }).model.provider).toBe("gateway");
    expect(resolveInstanceModel("google/gemini-3.5-flash-lite", options, anthropicKey).model.provider).toBe("gateway");
  });
});

describe("NVIDIA's free endpoints", () => {
  const nimKey = { AI_GATEWAY_API_KEY: "gateway-fixture", NVIDIA_API_KEY: "nvapi-fixture" };

  it("sends nim/ models to NVIDIA's chat completions with the key, under NVIDIA's model name", async () => {
    const fetch = rejectingFetch();
    vi.stubGlobal("fetch", fetch);
    const resolved = resolveInstanceModel("nim/moonshotai/kimi-k2.6", options, nimKey);
    expect(resolved.model.provider).not.toBe("gateway");
    expect(resolved.options.providerOptions).not.toHaveProperty("gateway");
    await expect(resolved.model.doGenerate(resolved.options)).rejects.toThrow();
    const [url, request] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("https://integrate.api.nvidia.com/v1/chat/completions");
    expect(new Headers(request.headers).get("authorization")).toBe("Bearer nvapi-fixture");
    const body = JSON.parse(request.body as string);
    expect(body.model).toBe("moonshotai/kimi-k2.6");
    expect(body).not.toHaveProperty("reasoning_effort");
  });

  it("moves screenshots out of tool results into the next user message", () => {
    const shot = { type: "file" as const, data: { type: "data" as const, data: "iVBORw0KGgo=" }, mediaType: "image/png" };
    const prompt = imagesOutOfToolResults([
      { role: "tool", content: [{ type: "tool-result", toolCallId: "c1", toolName: "browser_visual", output: { type: "content", value: [{ type: "text", text: "{\"url\":\"a\"}" }, shot] } }] },
    ]);
    expect(prompt).toEqual([
      { role: "tool", content: [{ type: "tool-result", toolCallId: "c1", toolName: "browser_visual", output: { type: "text", value: "{\"url\":\"a\"}\n(1 screenshot in the next message)" } }] },
      { role: "user", content: [{ type: "text", text: "Screenshots from the tool results above:" }, shot] },
    ]);
  });

  it("leaves nim/ models on the Gateway without a key", () => {
    expect(resolveInstanceModel("nim/moonshotai/kimi-k2.6", options, { AI_GATEWAY_API_KEY: "x" }).model.provider).toBe("gateway");
  });
});
