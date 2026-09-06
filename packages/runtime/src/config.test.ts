import { describe, expect, it } from "vitest";
import { loadRuntimeConfig } from "./config.js";

describe("runtime configuration", () => {
  it("keeps new services off the existing Go development port", () => {
    expect(loadRuntimeConfig("api", {}).port).toBe(8082);
    expect(loadRuntimeConfig("payments", {}).port).toBe(8083);
  });
  it.each(["-1", "65536", "eight", "80.5", "0"])("rejects invalid port %s", (PORT) => {
    expect(() => loadRuntimeConfig("api", { PORT })).toThrow();
  });
  it("rejects unrecognized deployment modes instead of assuming hosted", () => {
    expect(() => loadRuntimeConfig("api", { MISTY_DEPLOYMENT_MODE: "selfhost" })).toThrow();
  });
});
