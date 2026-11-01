import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { embedCall, modelCallFailure } = await import("../src/model-calls.js");

// OpenAI models call OpenAI directly with the device's key, so these calls are visible on the
// global fetch.
const providerFetch = vi.fn<typeof fetch>();
const route = { provider: "device" as const, reasoning: "low", credential: { provider: "openai" as const, apiKey: "openai-fixture" } };

function json(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

beforeEach(() => {
  providerFetch.mockReset();
  vi.stubGlobal("fetch", providerFetch);
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("model calls", () => {
  it("embeds with the requested dimensions", async () => {
    providerFetch.mockResolvedValue(
      json({ data: [{ embedding: [0.1, 0.2] }, { embedding: [0.3, 0.4] }], usage: { prompt_tokens: 9 } }),
    );
    const result = await embedCall({
      route,
      model: "openai/text-embedding-3-small",
      values: ["first", "second"],
      dimensions: 768,
    });
    expect(result).toEqual({ embeddings: [[0.1, 0.2], [0.3, 0.4]] });
    const [url, request] = providerFetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://api.openai.com/v1/embeddings");
    const body = JSON.parse(request.body as string);
    expect(body).toMatchObject({ model: "text-embedding-3-small", input: ["first", "second"], dimensions: 768 });
  });

  it("rejects malformed calls before any provider request", async () => {
    for (const body of [
      {},
      { route, model: "no-slash", messages: [], maxOutputTokens: 1 },
      { route, model: "openai/x", values: [], dimensions: 768 },
      { route: { provider: "openai", apiKey: "account-key" }, model: "openai/x", values: ["a"] },
    ]) {
      await expect(embedCall(body)).rejects.toMatchObject({ status: 400 });
    }
    expect(providerFetch).not.toHaveBeenCalled();
  });

  it("asks for a key when the device's key is for another provider", async () => {
    const other = { provider: "device" as const, credential: { provider: "anthropic" as const, apiKey: "sk-ant-fixture" } };
    const error = await embedCall({ route: other, model: "openai/text-embedding-3-small", values: ["a"] }).catch((e: unknown) => e);
    expect(modelCallFailure(error)).toEqual({ status: 409, body: { code: "model_key_needed", message: "Add a model key in Settings → Models." } });
    expect(providerFetch).not.toHaveBeenCalled();
  });

  it("keeps the key out of failures", async () => {
    const key = "sk-device-secret-fixture";
    providerFetch.mockResolvedValue(
      new Response(JSON.stringify({ error: { message: `Incorrect API key provided: ${key}` } }), {
        status: 401,
        headers: { "content-type": "application/json" },
      }),
    );
    const error = await embedCall({
      route: { provider: "device", credential: { provider: "openai", apiKey: key } },
      model: "openai/text-embedding-3-small",
      values: ["a"],
    }).catch((e: Error) => e);
    expect(error).toBeInstanceOf(Error);
    expect(JSON.stringify(modelCallFailure(error))).not.toContain(key);
  });

  it("reports provider failures without their response bodies", () => {
    const failure = modelCallFailure(Object.assign(new Error("secret echoed by provider"), { statusCode: 401 }));
    expect(failure).toEqual({
      status: 502,
      body: { code: "model_call_failed", message: "The model provider could not complete the request.", upstream_status: 401 },
    });
  });
});
