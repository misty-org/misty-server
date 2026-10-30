import { describe, expect, it } from "vitest";
import { modelFamilyNote } from "../src/model-family.js";

describe("model family notes", () => {
  it("gives Claude models their correction under any provider, and others none", () => {
    expect(modelFamilyNote("anthropic/claude-haiku-5.5")).toContain("look them up");
    expect(modelFamilyNote("openrouter/anthropic/claude-sonnet-5.5")).toContain("look them up");
    expect(modelFamilyNote("gateway/anthropic/claude-haiku-5.5")).toContain("look them up");
    expect(modelFamilyNote("openai/gpt-6-luna")).toBe("");
    expect(modelFamilyNote("xai/grok-5")).toBe("");
  });
});
