import { afterEach, describe, expect, it, vi } from "vitest";
import { followUpCall, restoreStepCall } from "../src/device-routing.js";
import { setDeviceModels } from "../src/device-models.js";

const models = {
  version: 1,
  providers: { ollama: { baseURL: "http://127.0.0.1:11434/v1" } },
  models: { agent: "ollama/qwen3:14b" },
};

/** A local Ollama that answers every call with the given text. */
const answers = (content: string) =>
  vi.fn(async () =>
    new Response(
      JSON.stringify({
        id: "chat_1",
        object: "chat.completion",
        created: 1_790_000_000,
        model: "qwen3:14b",
        choices: [{ index: 0, message: { role: "assistant", content }, finish_reason: "stop" }],
        usage: { prompt_tokens: 5, completion_tokens: 2, total_tokens: 7 },
      }),
      { status: 200, headers: { "content-type": "application/json" } },
    ),
  );

afterEach(() => {
  vi.unstubAllGlobals();
  setDeviceModels(null);
});

describe("follow-up routing", () => {
  it("routes a message on the device's fast model", async () => {
    setDeviceModels(models);
    vi.stubGlobal("fetch", answers('{"route":"queue"}'));
    await expect(followUpCall({ conversation: [], new_message: "Then plan a separate trip" })).resolves.toEqual({ route: "queue" });
  });

  it("steers when the answer is not one of the routes", async () => {
    setDeviceModels(models);
    vi.stubGlobal("fetch", answers("I would stop."));
    await expect(followUpCall({ conversation: [{ request: "Book it", response: "Booking" }], new_message: "Use Friday" })).resolves.toEqual({ route: "steer" });
  });

  it("asks for a model when the device has none", async () => {
    await expect(followUpCall({ conversation: [], new_message: "Stop" })).rejects.toThrow(/model_key_needed/);
  });
});

describe("restore steps", () => {
  const request = {
    url: "https://shop.test/checkout",
    goal: [{ label: "Size", kind: "text", value: { value: "M" } }],
    controls: [{ ref: "1", role: "field", label: "Size" }],
    history: [],
  };

  it("returns one action on a control the page has", async () => {
    setDeviceModels(models);
    vi.stubGlobal("fetch", answers('{"type":"type","ref":"1","value":"M"}'));
    await expect(restoreStepCall(request)).resolves.toEqual({ action: { type: "type", ref: "1", value: "M" } });
  });

  it("ends the restore on a control the page does not have, or a reply off the menu", async () => {
    setDeviceModels(models);
    vi.stubGlobal("fetch", answers('{"type":"click","ref":"9"}'));
    await expect(restoreStepCall(request)).resolves.toEqual({ action: { type: "done" } });
    vi.stubGlobal("fetch", answers('{"type":"submit","ref":"1"}'));
    await expect(restoreStepCall(request)).resolves.toEqual({ action: { type: "done" } });
  });
});
