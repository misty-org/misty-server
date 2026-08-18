import { describe, expect, it } from "vitest";
import { signRequest, verifyRequest } from "../src/signature.js";

describe("runtime request signatures", () => {
  const secret = Buffer.alloc(32, 7);
  const body = Buffer.from('{"run_id":"run_1"}');

  it("covers method, path, timestamp, and body", () => {
    const timestamp = "1770000000";
    const signature = signRequest(secret, "POST", "/v1/runs", timestamp, body);
    expect(verifyRequest({ secret, method: "POST", path: "/v1/runs", timestamp, signature, body, now: 1770000000 * 1000 })).toBe(true);
    expect(verifyRequest({ secret, method: "POST", path: "/v1/other", timestamp, signature, body, now: 1770000000 * 1000 })).toBe(false);
    expect(verifyRequest({ secret, method: "POST", path: "/v1/runs", timestamp, signature, body: Buffer.from("{}"), now: 1770000000 * 1000 })).toBe(false);
  });

  it("rejects stale requests", () => {
    const timestamp = "1770000000";
    const signature = signRequest(secret, "POST", "/v1/runs", timestamp, body);
    expect(verifyRequest({ secret, method: "POST", path: "/v1/runs", timestamp, signature, body, now: (1770000000 + 301) * 1000 })).toBe(false);
  });
});
