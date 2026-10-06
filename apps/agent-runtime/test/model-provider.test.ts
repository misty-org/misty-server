import { afterEach, describe, expect, it, vi } from "vitest";
import { WORKFLOW_DESERIALIZE, WORKFLOW_SERIALIZE } from "@workflow/serde";
import { InstanceModel } from "../src/instance-model.js";
import {
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
