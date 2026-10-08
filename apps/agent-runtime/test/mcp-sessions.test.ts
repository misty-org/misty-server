import { afterEach, expect, it } from "vitest";
import { cachedRunAccess, closeRunSession, rememberRunAccess, sessionCountForTests } from "../src/mcp-sessions.js";
import type { MCPRunAccess } from "../src/types.js";

const access = (token: string, expires = 300): MCPRunAccess => ({ access_token: token, token_type: "Bearer", expires_in: expires, mcp_path: "/mcp", protocol: "2026-07-28" });
const base = "http://api:8080/api";

afterEach(() => {
  closeRunSession("run-a", base);
  closeRunSession("run-b", base);
});

it("reuses a run's bearer until a minute before it expires", () => {
  const now = 1_000_000;
  rememberRunAccess("run-a", base, access("t1"), now);
  expect(cachedRunAccess("run-a", base, now + 60_000)?.access_token).toBe("t1");
  expect(cachedRunAccess("run-a", base, now + 240_001)).toBeUndefined();
  rememberRunAccess("run-a", base, access("t2"), now + 240_001);
  expect(cachedRunAccess("run-a", base, now + 240_002)?.access_token).toBe("t2");
});

it("never shares a bearer across runs or control planes", () => {
  rememberRunAccess("run-a", base, access("a"));
  expect(cachedRunAccess("run-b", base)).toBeUndefined();
  expect(cachedRunAccess("run-a", "http://other:8080")).toBeUndefined();
  rememberRunAccess("run-b", base, access("b"));
  expect(cachedRunAccess("run-a", base)?.access_token).toBe("a");
  expect(cachedRunAccess("run-b", base)?.access_token).toBe("b");
});

it("forgets a session once it is closed", () => {
  rememberRunAccess("run-a", base, access("a"));
  const before = sessionCountForTests();
  closeRunSession("run-a", base);
  expect(sessionCountForTests()).toBe(before - 1);
  expect(cachedRunAccess("run-a", base)).toBeUndefined();
});

it("does not cache a bearer that is already about to expire", () => {
  rememberRunAccess("run-a", base, access("short", 30));
  expect(cachedRunAccess("run-a", base)).toBeUndefined();
});
