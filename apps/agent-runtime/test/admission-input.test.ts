import { describe, expect, it } from "vitest";
import { admissionInput } from "../src/admission-input.js";

describe("admissionInput", () => {
  it("counts a screenshot as one image, not megabytes of text", () => {
    const screenshot = "data:image/png;base64," + "A".repeat(3_000_000);
    const result = admissionInput({ messages: [{ role: "user", content: "what group is this?" }, { role: "tool", content: [{ type: "image", image: screenshot }] }] });
    expect(result.input_images).toBe(1);
    expect(result.input_bytes).toBeLessThan(1_000);
  });

  it("counts raw image bytes as an image", () => {
    const result = admissionInput({ messages: [{ type: "file", data: new Uint8Array(500_000) }] });
    expect(result.input_images).toBe(1);
    expect(result.input_bytes).toBeLessThan(1_000);
  });

  it("keeps plain text at its real size and reports no images", () => {
    const result = admissionInput({ system: "be brief", messages: [{ role: "user", content: "word ".repeat(200) }] });
    expect(result.input_images).toBeUndefined();
    expect(result.input_bytes).toBeGreaterThan(1_000);
  });
});
