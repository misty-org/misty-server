import { describe, expect, it } from "vitest";
import { modelFamily, modelFamilyNote } from "../src/model-family.js";

describe("model family notes", () => {
  it("names the family from the gateway model id", () => {
    expect(modelFamily("openai/gpt-6-luna")).toBe("openai");
    expect(modelFamily("anthropic/claude-haiku-5.5")).toBe("anthropic");
    expect(modelFamily("google/gemini-3.5-flash-lite")).toBe("google");
    expect(modelFamily("xai/grok-5")).toBe("other");
  });

  it("gives each family only its own correction", () => {
    expect(modelFamilyNote("openai/gpt-6-luna")).toContain("economical");
    expect(modelFamilyNote("anthropic/claude-haiku-5.5")).toContain("look them up");
    expect(modelFamilyNote("xai/grok-5")).toBe("");
  });
});
