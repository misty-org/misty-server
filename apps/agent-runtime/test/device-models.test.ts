import { afterEach, describe, expect, it } from "vitest";
import { deviceModelId, deviceRoute, deviceSecrets, setDeviceModels } from "../src/device-models.js";
import { resolveTarget } from "../src/model-provider.js";

const models = {
  version: 1,
  providers: { openrouter: { apiKey: "sk-or-device-key" }, ollama: { baseURL: "http://127.0.0.1:11434/v1" } },
  models: { agent: "openrouter/anthropic/claude-sonnet-5.5", embedding: "ollama/nomic-embed-text" },
  reasoning: "medium",
};

afterEach(() => setDeviceModels(null));

describe("device models", () => {
  it("keeps the device's choices and keys in memory", () => {
    expect(setDeviceModels(models)).toBe(true);
    expect(deviceModelId("agent")).toBe("openrouter/anthropic/claude-sonnet-5.5");
    expect(deviceModelId("fast")).toBe("openrouter/anthropic/claude-sonnet-5.5");
    expect(deviceSecrets()).toEqual(["sk-or-device-key"]);
    const route = deviceRoute("openrouter/anthropic/claude-sonnet-5.5");
    expect(route).toMatchObject({ provider: "device", reasoning: "medium", credential: { provider: "openrouter", apiKey: "sk-or-device-key" } });
  });

  it("refuses a body that isn't the app's models", () => {
    expect(setDeviceModels({ version: 2, providers: {}, models: {} })).toBe(false);
    expect(setDeviceModels({ version: 1, providers: { unknown: { apiKey: "x" } }, models: {} })).toBe(false);
  });

  it("asks for a key when the device has none for the model's provider", () => {
    setDeviceModels(models);
    expect(() => deviceRoute("anthropic/claude-sonnet-5-5")).toThrow(/model_key_needed/);
  });

  it("lets a device route reach a model server on the person's own computer", () => {
    setDeviceModels(models);
    const target = resolveTarget("ollama/nomic-embed-text", deviceRoute("ollama/nomic-embed-text"));
    expect(target.credential.baseURL).toBe("http://127.0.0.1:11434/v1");
  });

  it("clears with an empty body", () => {
    setDeviceModels(models);
    expect(setDeviceModels({})).toBe(true);
    expect(deviceModelId("agent")).toBeUndefined();
  });
});
