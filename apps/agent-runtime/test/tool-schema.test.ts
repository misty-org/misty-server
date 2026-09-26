import { describe, expect, it } from "vitest";
import { Ajv } from "ajv";
import { normalizeToolInputSchema } from "../src/tool-schema.js";

const ajv = new Ajv();
describe("legacy Go tool catalog compatibility", () => {
  it("lets the runtime compile a no-argument tool whose Go slice was nil", () => {
    const input = { type: "object", properties: {}, required: null, additionalProperties: false };
    expect(() => ajv.compile(input)).toThrow("required must be array");
    const validate = ajv.compile(normalizeToolInputSchema(input));
    expect(validate({})).toBe(true);
    expect(validate({ unexpected: true })).toBe(false);
    expect(input.required).toBeNull();
  });
  it("preserves required fields and property constraints", () => {
    const input = { type: "object", properties: { operation: { type: "string", enum: ["create", "update"] } }, required: ["operation"], additionalProperties: false };
    expect(normalizeToolInputSchema(input)).toBe(input);
    const validate = ajv.compile(normalizeToolInputSchema(input));
    expect(validate({})).toBe(false);
    expect(validate({ operation: "delete" })).toBe(false);
    expect(validate({ operation: "create" })).toBe(true);
  });
  it("does not silently weaken malformed constraints", () => {
    const input = { type: "object", required: "operation" };
    expect(() => ajv.compile(normalizeToolInputSchema(input))).toThrow();
  });
});
