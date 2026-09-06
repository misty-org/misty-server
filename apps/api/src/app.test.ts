import { describe, expect, it } from "vitest";
import { createApi } from "./app.js";
import { createLogger } from "../../../packages/runtime/src/logger.js";
import { loadRuntimeConfig } from "../../../packages/runtime/src/config.js";

const logger = createLogger(loadRuntimeConfig("api", { LOG_LEVEL: "silent" }));
function createTestApi(overrides: Partial<Parameters<typeof createApi>[0]> = {}) {
  return createApi({ logger, checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false, ...overrides });
}

describe("API readiness", () => {
  it("does not claim readiness before route parity even when PostgreSQL is healthy", async () => {
    const app = createTestApi();
    expect((await app.request("/livez")).status).toBe(200);
    const response = await app.request("/readyz");
    expect(response.status).toBe(503);
    expect(await response.json()).toMatchObject({ status: "migrating" });
    expect(response.headers.get("cache-control")).toBe("no-store");
  });
  it("keeps database error details out of readiness responses", async () => {
    const app = createTestApi({ checkDatabase: async () => { throw new Error("password=private-value"); } });
    const response = await app.request("/readyz");
    expect(response.status).toBe(503);
    expect(await response.text()).not.toContain("private-value");
  });
  it("withdraws readiness while draining", async () => {
    const response = await createTestApi({ isDraining: () => true, migrationComplete: true }).request("/readyz");
    expect(response.status).toBe(503);
    expect(await response.json()).toMatchObject({ status: "draining" });
  });
  it("generates correlation IDs rather than trusting a caller's header", async () => {
    const response = await createTestApi().request("/livez", { headers: { "X-Request-ID": "forged" } });
    expect(response.headers.get("x-request-id")).toMatch(/^[a-f0-9-]{36}$/);
  });
  it("does not expose arbitrary thrown errors", async () => {
    const app = createTestApi();
    app.get("/failure", () => { throw new Error("secret-token"); });
    const response = await app.request("/failure");
    expect(response.status).toBe(500);
    expect(await response.json()).toEqual({ code: "internal_error", message: "An internal error occurred." });
  });
  it("provides standard HEAD behavior for liveness", async () => {
    const response = await createTestApi().request("/livez", { method: "HEAD" });
    expect(response.status).toBe(200);
    expect(await response.text()).toBe("");
  });
});
