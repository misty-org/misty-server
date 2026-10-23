import { describe, expect, it } from "vitest";
import { modelFamilyNote } from "../src/model-family.js";

describe("model family notes", () => {
  it("gives the house model's family its correction and others none", () => {
    expect(modelFamilyNote("anthropic/claude-haiku-5.5")).toContain("look them up");
    expect(modelFamilyNote("openai/gpt-6-luna")).toBe("");
    expect(modelFamilyNote("xai/grok-5")).toBe("");
  });
});
