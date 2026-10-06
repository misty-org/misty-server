import type * as SignatureModule from "../src/signature.js";
import type { AddressInfo } from "node:net";
import { expect, it, vi } from "vitest";

vi.mock("../src/model-provider.js", () => ({ instanceModelConfig: vi.fn() }));
vi.mock("../src/vercel-harness.js", () => ({ vercelHarness: { version: "test" } }));
vi.mock("../src/signature.js", async (importOriginal) => ({
  ...await importOriginal<typeof SignatureModule>(),
  decodeControlSecret: () => Buffer.alloc(32),
  verifyRequest: vi.fn(() => false),
}));

it("shares a throttle across control routes before parsing or authentication", async () => {
  const { default: app } = await import("../src/index.js");
  const { verifyRequest } = await import("../src/signature.js");
  const server = app.listen(0, "127.0.0.1");
  await new Promise<void>((resolve) => server.once("listening", resolve));
  const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  try {
    for (let attempt = 0; attempt < 600; attempt++) {
      const response = await fetch(`${base}/v1/runs`, { method: "POST" });
      expect(response.status).toBe(401);
      await response.arrayBuffer();
    }
    for (const route of ["runs", "runs/id/status", "runs/id/cancel", "devices/token"]) {
      const response = await fetch(`${base}/v1/${route}`, {
        method: "POST",
        headers: { "content-type": "application/json", "x-forwarded-for": "192.0.2.123" },
        body: "invalid json",
      });
      expect(response.status).toBe(429);
      expect(response.headers.get("retry-after")).toBeTruthy();
      expect(await response.json()).toEqual({ code: "rate_limited" });
    }
    expect(verifyRequest).toHaveBeenCalledTimes(600);
    const health = await fetch(`${base}/health`);
    expect(health.status).toBe(200);
    await health.arrayBuffer();
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
}, 30_000);
