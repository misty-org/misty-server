import { afterEach, describe, expect, it, vi } from "vitest";
import { modelTestCall } from "../src/device-calls.js";
import { setDeviceModels } from "../src/device-models.js";

const reply = (message: Record<string, unknown>) =>
  new Response(
    JSON.stringify({
      id: "chat_1",
      object: "chat.completion",
      created: 1_790_000_000,
      model: "qwen3:14b",
      choices: [
        {
          index: 0,
          message: { role: "assistant", ...message },
          finish_reason: message.tool_calls ? "tool_calls" : "stop",
        },
      ],
      usage: { prompt_tokens: 5, completion_tokens: 2, total_tokens: 7 },
    }),
    { status: 200, headers: { "content-type": "application/json" } },
  );

/** A local Ollama that answers the two checks as a capable model would, or ignores tools and images. */
function fakeOllama(capable: boolean) {
  return vi.fn(async (_url: string, init: RequestInit) => {
    const body = JSON.parse(String(init.body)) as {
      messages: Array<{ role: string; content: unknown }>;
      tools?: Array<{ function: { name: string } }>;
    };
    const text = JSON.stringify(body.messages);
    if (text.includes("image_url"))
      return reply({ content: capable ? "Red." : "I can't see images." });
    const stock = body.tools?.some((tool) => tool.function.name === "stock");
    if (stock && !body.messages.some((message) => message.role === "tool") && capable)
      return reply({
        content: null,
        tool_calls: [
          {
            id: "call_1",
            type: "function",
            function: {
              name: "stock",
              arguments: JSON.stringify({ store: "North", product: "blue kettle" }),
            },
          },
        ],
      });
    return reply({ content: capable ? "The North store has 37." : "I don't know." });
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
  setDeviceModels(null);
});

describe("model test", () => {
  const models = {
    version: 1,
    providers: { ollama: { baseURL: "http://127.0.0.1:11434/v1" } },
    models: { agent: "ollama/qwen3:14b" },
  };

  it("passes a model that calls tools and sees screenshots", async () => {
    setDeviceModels(models);
    vi.stubGlobal("fetch", fakeOllama(true));
    const result = await modelTestCall({});
    expect(result.model).toBe("ollama/qwen3:14b");
    expect(result.tools.ok).toBe(true);
    expect(result.images.ok).toBe(true);
  });

  it("says which check a weaker model fails", async () => {
    setDeviceModels(models);
    vi.stubGlobal("fetch", fakeOllama(false));
    const result = await modelTestCall({});
    expect(result.tools).toMatchObject({
      ok: false,
      detail: "It answered without calling the tool.",
    });
    expect(result.images.ok).toBe(false);
  });

  it("asks for a model when the device has none", async () => {
    await expect(modelTestCall({})).rejects.toThrow(/model_key_needed/);
  });
});
