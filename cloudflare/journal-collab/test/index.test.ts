import { describe, expect, it } from "vitest";

import { responseWithRequestID } from "../src/response";

describe("worker response correlation", () => {
  it("clones ordinary Durable Object responses before changing headers", async () => {
    const original = new Response('{"ok":true}', {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });

    const correlated = responseWithRequestID(original, "worker_request_123");

    expect(correlated).not.toBe(original);
    expect(correlated.headers.get("X-Request-ID")).toBe("worker_request_123");
    expect(original.headers.get("X-Request-ID")).toBeNull();
    await expect(correlated.json()).resolves.toEqual({ ok: true });
  });
});
