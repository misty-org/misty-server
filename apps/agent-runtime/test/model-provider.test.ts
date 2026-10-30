import { afterEach, describe, expect, it, vi } from "vitest";
import { WORKFLOW_DESERIALIZE, WORKFLOW_SERIALIZE } from "@workflow/serde";
import type { LanguageModelV4CallOptions } from "@ai-sdk/provider";

const controlPlaneRequest = vi.fn();
vi.mock("../src/control-plane.js", () => ({ controlPlaneRequest }));

const { InstanceModel } = await import("../src/instance-model.js");
const {
  imagesOutOfToolResults,
  instanceModelConfig,
  parseModelId,
  resolveEmbeddingModel,
  resolveInstanceModel,
  resolveModel,
  resolveSpeechModel,
  resolveTranscriptionModel,
  ModelSetupError,
} = await import("../src/model-provider.js");
type ModelRoute = import("../src/model-provider.js").ModelRoute;
type ProviderId = import("../src/model-provider.js").ProviderId;

const options: LanguageModelV4CallOptions = {
  prompt: [{ role: "user", content: [{ type: "text", text: "hello" }] }],
  providerOptions: { gateway: { models: ["other/provider"] } },
  reasoning: "high",
};
const withKey = { AI_GATEWAY_API_KEY: "gateway-fixture", OPENAI_API_KEY: "openai-fixture" };
const instance: ModelRoute = { provider: "instance" };
const account = (provider: ProviderId, apiKey: string, baseURL?: string): ModelRoute => ({
  provider: "account",
  credential: { provider, apiKey, ...(baseURL ? { baseURL } : {}) },
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  controlPlaneRequest.mockReset();
});

function rejectingFetch(body = '{"error":{"message":"fixture rejection"}}') {
  return vi.fn<typeof globalThis.fetch>(
    async () => new Response(body, { status: 401, headers: { "content-type": "application/json" } }),
  );
}

/** Calls the model once against a rejecting fetch and returns what it sent. */
async function sent(resolved: { model: { doGenerate(options: LanguageModelV4CallOptions): PromiseLike<unknown> }; options: LanguageModelV4CallOptions }) {
  const fetch = rejectingFetch();
  vi.stubGlobal("fetch", fetch);
  await expect(resolved.model.doGenerate(resolved.options)).rejects.toThrow();
  const [url, request] = fetch.mock.calls[0] as unknown as [string, RequestInit];
  return { url: String(url), headers: new Headers(request.headers), body: JSON.parse(String(request.body)) };
}

describe("model IDs", () => {
  it("reads provider/model and the old aliases", () => {
    expect(parseModelId("openrouter/anthropic/claude-sonnet-5.5")).toEqual({ provider: "openrouter", model: "anthropic/claude-sonnet-5.5" });
    expect(parseModelId("ollama/qwen3:14b")).toEqual({ provider: "ollama", model: "qwen3:14b" });
    expect(parseModelId("nim/moonshotai/kimi-k2.6")).toEqual({ provider: "nvidia", model: "moonshotai/kimi-k2.6" });
    expect(parseModelId("gemini/gemini-3.1-flash-lite")).toEqual({ provider: "google", model: "gemini-3.1-flash-lite" });
    for (const model of ["example", "openai/", "openai/has space", "mystery/model"]) expect(() => parseModelId(model)).toThrow(ModelSetupError);
  });

  it("rejects invalid instance models", () => {
    for (const model of ["example", "openai/", "openai/has space", "mystery/x"])
      expect(() => instanceModelConfig({ MISTY_AGENT_MODEL: model })).toThrow();
    expect(instanceModelConfig({})).toEqual({ provider: "instance", model: "" });
  });
});

describe("instance routes", () => {
  it("falls back to the operator's Gateway key for makers the Gateway serves", () => {
    const result = resolveInstanceModel("openai/example", options, { AI_GATEWAY_API_KEY: "fixture" });
    expect(result.model.provider).toBe("gateway");
    expect(result.model.modelId).toBe("openai/example");
    expect(result.options).toEqual(options);
    expect(resolveInstanceModel("anthropic/claude-haiku-5.5", options, { AI_GATEWAY_API_KEY: "x" }).model.provider).toBe("gateway");
  });

  it("asks for a key when the instance has none for the provider", () => {
    for (const model of ["openai/x", "nvidia/x", "openrouter/x", "openai-compatible/x"])
      expect(() => resolveInstanceModel(model, options, {})).toThrow("model_key_needed");
    // NVIDIA, OpenRouter and local servers aren't on the Gateway under these names.
    expect(() => resolveInstanceModel("nim/moonshotai/kimi-k2.6", options, { AI_GATEWAY_API_KEY: "x" })).toThrow("model_key_needed");
  });

  it("sends OpenAI models to OpenAI with the instance key", async () => {
    const resolved = resolveInstanceModel("openai/gpt-6-luna", options, withKey);
    expect(resolved.model.provider).not.toBe("gateway");
    expect(resolved.options.providerOptions).not.toHaveProperty("gateway");
    const request = await sent(resolved);
    expect(request.url).toBe("https://api.openai.com/v1/responses");
    expect(request.headers.get("authorization")).toBe("Bearer openai-fixture");
    expect(request.body.model).toBe("gpt-6-luna");
    expect(request.body.reasoning.effort).toBe("high");
  });

  it("keeps optional browser arguments optional on the Responses wire", async () => {
    const toolOptions: LanguageModelV4CallOptions = {
      ...options,
      tools: [
        {
          type: "function",
          name: "browser_inspect",
          inputSchema: {
            type: "object",
            properties: { scopeId: { type: "string" }, extensionId: { type: "integer", minimum: 1 } },
            required: ["scopeId"],
            additionalProperties: false,
          },
        },
        {
          type: "function",
          name: "explicit_strict",
          strict: true,
          inputSchema: { type: "object", properties: {}, required: [], additionalProperties: false },
        },
      ],
    };
    const { body } = await sent(resolveInstanceModel("openai/gpt-6-luna", toolOptions, withKey));
    expect(body.tools[0].strict).toBe(false);
    expect(body.tools[0].parameters.required).toEqual(["scopeId"]);
    expect(body.tools[1].strict).toBe(true);
    expect(toolOptions.tools?.[0]).not.toHaveProperty("strict");
  });

  it("sends Anthropic models to Anthropic with the instance key, in Anthropic's own naming", async () => {
    const resolved = resolveInstanceModel("anthropic/claude-haiku-5.5", options, { AI_GATEWAY_API_KEY: "g", CLAUDE_API_KEY: "anthropic-fixture" });
    expect(resolved.options.providerOptions).not.toHaveProperty("gateway");
    const request = await sent(resolved);
    expect(request.url).toBe("https://api.anthropic.com/v1/messages");
    expect(request.headers.get("x-api-key")).toBe("anthropic-fixture");
    expect(request.body.model).toBe("claude-haiku-5-5");
  });

  it("runs Ollama locally without a key", async () => {
    const request = await sent(resolveInstanceModel("ollama/qwen3:14b", options, {}));
    expect(request.url).toBe("http://host.docker.internal:11434/v1/chat/completions");
    expect(request.headers.get("authorization")).toBeNull();
    expect(request.body.model).toBe("qwen3:14b");
  });

  it("applies the same rules to embeddings, speech and transcription", async () => {
    expect((await resolveEmbeddingModel("openai/text-embedding-3-small", instance, withKey)).provider).not.toBe("gateway");
    expect((await resolveEmbeddingModel("google/gemini-embedding-001", instance, withKey)).provider).toBe("gateway");
    expect((await resolveEmbeddingModel("google/gemini-embedding-001", instance, { GEMINI_API_KEY: "g" })).provider).toMatch(/^google/);
    expect((await resolveEmbeddingModel("openai/text-embedding-3-small", instance, { AI_GATEWAY_API_KEY: "g" })).provider).toBe("gateway");
    expect((await resolveTranscriptionModel("openai/whisper-1", instance, withKey)).provider).not.toBe("gateway");
    expect((await resolveTranscriptionModel("openai/whisper-1", instance, { AI_GATEWAY_API_KEY: "g" })).provider).toBe("gateway");
    expect((await resolveTranscriptionModel("soniox/stt-async-v4", instance, { SONIOX_API_KEY: "s" })).provider).toMatch(/soniox/);
    expect((await resolveSpeechModel("elevenlabs/eleven_flash_v2_5", instance, { ELEVENLABS_API_KEY: "e" })).provider).toMatch(/elevenlabs/);
    await expect(resolveSpeechModel("elevenlabs/eleven_flash_v2_5", instance, {})).rejects.toThrow("model_key_needed");
    await expect(resolveEmbeddingModel("anthropic/x", instance, { ANTHROPIC_API_KEY: "a" })).rejects.toThrow("model_unsupported");
  });
});

describe("account routes", () => {
  const env = { OPENAI_API_KEY: "instance-openai", AI_GATEWAY_API_KEY: "instance-gateway", NVIDIA_API_KEY: "instance-nvidia" };

  it("uses exactly the account's key and never the instance's", async () => {
    const request = await sent(resolveModel("openai/gpt-6-luna", options, account("openai", "sk-account"), env));
    expect(request.headers.get("authorization")).toBe("Bearer sk-account");
    expect(() => resolveModel("openai/gpt-6-luna", options, account("anthropic", "sk-ant"), env)).toThrow("model_key_needed");
    expect(() => resolveModel("openai/gpt-6-luna", options, account("openai", " "), env)).toThrow("model_key_needed");
  });

  it.each([
    ["anthropic/claude-sonnet-5.5", account("anthropic", "ak"), "https://api.anthropic.com/v1/messages", ["x-api-key", "ak"]],
    ["google/gemini-3.5-flash", account("google", "gk"), "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:generateContent", ["x-goog-api-key", "gk"]],
    ["openrouter/anthropic/claude-sonnet-5.5", account("openrouter", "ork"), "https://openrouter.ai/api/v1/chat/completions", ["authorization", "Bearer ork"]],
    ["nvidia/moonshotai/kimi-k2.6", account("nvidia", "nvk"), "https://integrate.api.nvidia.com/v1/chat/completions", ["authorization", "Bearer nvk"]],
    ["nim/moonshotai/kimi-k2.6", account("nvidia", "nvk"), "https://integrate.api.nvidia.com/v1/chat/completions", ["authorization", "Bearer nvk"]],
    ["ollama/qwen3:14b", account("ollama", "", "https://ollama.example.com/v1"), "https://ollama.example.com/v1/chat/completions", ["authorization", null]],
    ["openai-compatible/my-model", account("openai-compatible", "ck", "https://llm.example.com/v1/"), "https://llm.example.com/v1/chat/completions", ["authorization", "Bearer ck"]],
  ] as const)("sends %s to its provider", async (modelId, route, url, [header, value]) => {
    const resolved = resolveModel(modelId, options, route, env);
    expect(resolved.options.providerOptions).not.toHaveProperty("gateway");
    const request = await sent(resolved);
    expect(request.url).toBe(url);
    expect(request.headers.get(header)).toBe(value);
  });

  it("drops reasoning_effort for servers that may refuse it, and keeps it for OpenRouter", async () => {
    expect((await sent(resolveModel("nvidia/x", options, account("nvidia", "k"), env))).body).not.toHaveProperty("reasoning_effort");
    expect((await sent(resolveModel("openrouter/x", options, account("openrouter", "k"), env))).body.reasoning_effort).toBe("high");
  });

  it("runs Gateway models on the account's own Gateway key", async () => {
    const resolved = resolveModel("gateway/anthropic/claude-sonnet-5.5", { ...options, providerOptions: { gateway: { models: ["gateway/anthropic/claude-sonnet-5.5"] } } }, account("gateway", "vck"), env);
    expect(resolved.model.provider).toBe("gateway");
    expect(resolved.model.modelId).toBe("anthropic/claude-sonnet-5.5");
    expect(resolved.options.providerOptions?.gateway).toEqual({ models: ["anthropic/claude-sonnet-5.5"] });
    const fetch = rejectingFetch();
    vi.stubGlobal("fetch", fetch);
    await expect(resolved.model.doGenerate(resolved.options)).rejects.toThrow();
    const auth = fetch.mock.calls.map(([, init]) => new Headers((init as RequestInit).headers).get("authorization"));
    expect(auth).toContain("Bearer vck");
    expect(auth.join()).not.toContain("instance-gateway");
  });

  it("needs a base URL for OpenAI-compatible servers and refuses private ones", () => {
    expect(() => resolveModel("openai-compatible/x", options, account("openai-compatible", "k"), env)).toThrow("model_base_url_invalid");
    for (const baseURL of ["http://api:8080", "http://127.0.0.1:11434/v1", "http://metadata.google.internal", "http://[::1]:3000", "https://user:pw@llm.example.com"])
      expect(() => resolveModel("openai-compatible/x", options, account("openai-compatible", "k", baseURL), env)).toThrow("model_base_url_invalid");
    expect(() => resolveModel("ollama/x", options, account("ollama", "", "http://host.docker.internal:11434/v1"), env)).not.toThrow();
  });

  it("has no language models for speech and transcription providers", () => {
    expect(() => resolveModel("elevenlabs/x", options, account("elevenlabs", "ek"), env)).toThrow("model_unsupported");
  });
});

describe("credentials stay out of durable state", () => {
  const identity = { mistyRunId: "run_1", runtimeRunId: "wrun_1", controlPlaneURL: "http://api:8080" };
  const key = "sk-route-secret-fixture-123";

  it("serializes only public model and run identity", () => {
    const serialized = InstanceModel[WORKFLOW_SERIALIZE](new InstanceModel("openai/example"));
    expect(serialized).toEqual({ modelId: "openai/example", identity: undefined, role: "agent" });
    expect(InstanceModel[WORKFLOW_DESERIALIZE](serialized).modelId).toBe("openai/example");
  });

  it("reads a route's key inside the model step and never writes it to the step's args, model or error", async () => {
    controlPlaneRequest.mockResolvedValue({ provider: "account", reasoning: "low", credential: { provider: "openai", apiKey: key } });
    const fetch = rejectingFetch(JSON.stringify({ error: { message: `Incorrect API key provided: ${key}` } }));
    vi.stubGlobal("fetch", fetch);
    const model = new InstanceModel("openai/gpt-6-luna", identity);
    const args = structuredClone(options);
    const error = await model.doGenerate(args).catch((caught: unknown) => caught);
    // The key was used for the call itself.
    expect(new Headers((fetch.mock.calls[0]![1] as RequestInit).headers).get("authorization")).toBe(`Bearer ${key}`);
    expect(controlPlaneRequest).toHaveBeenCalledWith(identity, "model-provider", { role: "agent", model: "openai/gpt-6-luna" }, "agent:provider");
    // What the workflow persists for the step: the model descriptor, its arguments and its failure.
    const seen = new WeakSet<object>();
    const persisted = JSON.stringify([InstanceModel[WORKFLOW_SERIALIZE](model), model, args, error], (_key, value) => {
      if (value && typeof value === "object") {
        if (seen.has(value)) return undefined;
        seen.add(value);
      }
      return value;
    });
    expect(persisted).not.toContain(key);
    expect(String((error as Error).message) + String((error as Error).stack)).not.toContain(key);
  });

  it("fails the step for good when there is no key", async () => {
    controlPlaneRequest.mockResolvedValue({ provider: "account", credential: { provider: "anthropic", apiKey: key } });
    const error = await new InstanceModel("openai/gpt-6-luna", identity).doGenerate(options).then(() => new Error("resolved"), (caught: Error) => caught);
    expect(error.name).toBe("FatalError");
    expect(error.message).toContain("model_key_needed");
    expect(error.message).not.toContain(key);
  });

  it("refuses a malformed route without repeating it", async () => {
    controlPlaneRequest.mockResolvedValue({ provider: "account", credential: { provider: "mystery", apiKey: key } });
    const error = await new InstanceModel("openai/gpt-6-luna", identity).doGenerate(options).then(() => new Error("resolved"), (caught: Error) => caught);
    expect(error.message).toContain("invalid_model_route");
    expect(error.message).not.toContain(key);
  });
});

describe("chat-completions providers", () => {
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

  it("sends nim/ models to NVIDIA under NVIDIA's model name, without reasoning_effort", async () => {
    const request = await sent(resolveInstanceModel("nim/moonshotai/kimi-k2.6", options, { NVIDIA_API_KEY: "nvapi-fixture" }));
    expect(request.url).toBe("https://integrate.api.nvidia.com/v1/chat/completions");
    expect(request.headers.get("authorization")).toBe("Bearer nvapi-fixture");
    expect(request.body.model).toBe("moonshotai/kimi-k2.6");
    expect(request.body).not.toHaveProperty("reasoning_effort");
  });
});
