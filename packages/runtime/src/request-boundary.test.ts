import { Hono } from "hono";
import { expect, it } from "vitest";
import { createRequestBoundary } from "./request-boundary.js";

it("ignores spoofed forwarding headers from direct peers and resolves a trusted chain from the right", async () => {
  let remote = "203.0.113.9";
  const boundary = createRequestBoundary({ trustProxyHeaders: true, peerAddress: () => remote });
  const app = new Hono().get("/", (c) => c.json({ ip: boundary.clientIp(c), secure: boundary.secure(c) }));
  const headers = { "X-Forwarded-For": "192.0.2.111, 198.51.100.10, 10.1.2.3", "X-Forwarded-Proto": "https" };
  expect(await (await app.request("/", { headers })).json()).toEqual({ ip: remote, secure: false });
  remote = "127.0.0.1";
  expect(await (await app.request("/", { headers })).json()).toEqual({ ip: "198.51.100.10", secure: true });
  remote = "::ffff:127.0.0.1";
  expect(await (await app.request("/", { headers })).json()).toEqual({ ip: "198.51.100.10", secure: true });
  expect(await (await app.request("/", { headers: { ...headers, "X-Forwarded-For": "198.51.100.10, malformed" } })).json()).toEqual({ ip: remote, secure: true });
});
it("preserves configured/desktop origins and rejects cross-origin mutations before dispatch", async () => {
  const boundary = createRequestBoundary({ allowedOrigins: ["https://misty.example"] });
  let mutations = 0;
  const app = new Hono().use("*", boundary.middleware).post("/", (c) => { mutations++; return c.json({ ok: true }); });
  for (const origin of ["tauri://localhost", "http://localhost:5199", "https://misty.example"]) {
    const response = await app.request("/", { method: "POST", headers: { Origin: origin } });
    expect(response.status).toBe(200);
    expect(response.headers.get("Access-Control-Allow-Origin")).toBe(origin);
  }
  expect((await app.request("/", { method: "POST", headers: { Origin: "https://attacker.invalid" } })).status).toBe(403);
  expect((await app.request("/", { method: "POST", headers: { "Sec-Fetch-Site": "cross-site" } })).status).toBe(403);
  expect(mutations).toBe(3);
  const preflight = await app.request("/", { method: "OPTIONS", headers: { Origin: "tauri://localhost", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type,x-misty-self-hosted-entitlement" } });
  expect(preflight.status).toBe(204);
  expect(preflight.headers.get("Access-Control-Allow-Credentials")).toBe("true");
});
it("rejects invalid origin and proxy configuration", () => {
  expect(() => createRequestBoundary({ allowedOrigins: ["https://*.example.com"] })).toThrow();
  expect(() => createRequestBoundary({ trustedProxyCidrs: ["0.0.0.0/99"] })).toThrow();
  for (const invalid of ["127.0.0.1/", "127.0.0.1/8/ignored", "127.0.0.1/1e1", "::1/ "]) {
    expect(() => createRequestBoundary({ trustedProxyCidrs: [invalid] })).toThrow();
  }
});
