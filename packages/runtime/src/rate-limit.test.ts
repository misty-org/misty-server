import { expect, it } from "vitest";
import { createSlidingWindowLimiter } from "./rate-limit.js";

it("enforces a sliding window and reclaims expired identities within a bounded map", () => {
  let now = 0;
  const limiter = createSlidingWindowLimiter({ limit: 2, windowMilliseconds: 60000, maxKeys: 2, now: () => now });
  expect(limiter.allow("a").allowed).toBe(true);
  now = 1000;
  expect(limiter.allow("a").allowed).toBe(true);
  expect(limiter.allow("a")).toEqual({ allowed: false, retrySeconds: 59 });
  expect(limiter.allow("b").allowed).toBe(true);
  expect(limiter.allow("sprayed-key").allowed).toBe(false);
  now = 61000;
  expect(limiter.allow("sprayed-key").allowed).toBe(true);
});
