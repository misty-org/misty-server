import { createHash } from "node:crypto";
import { expect, it, vi } from "vitest";
import { authorizationUrl, connectionProvider, grantedAccess, requestedAccess } from "./catalog.js";
import { loadConnectionAuthorizationConfig } from "./config.js";
import { createConnectionIdentityReader } from "./identity.js";
import { connectionAuthorizationInput } from "./service.js";

it("pins callback origins and API prefixes instead of accepting caller-controlled redirects", () => {
  for (const input of ["https://api.example.invalid", "https://api.example.invalid/", "https://api.example.invalid/api/"]) expect(loadConnectionAuthorizationConfig({ MISTY_PUBLIC_API_URL: input })).toEqual({ apiBase: "https://api.example.invalid/api" });
  expect(loadConnectionAuthorizationConfig({ MISTY_PUBLIC_API_URL: "http://localhost:8082/v1" })).toEqual({ apiBase: "http://localhost:8082/v1" });
  expect(loadConnectionAuthorizationConfig({})).toBeNull();
  for (const input of ["http://localhost.attacker.invalid", "http://127.0.0.2", "https://user:secret@api.example.invalid", "https://api.example.invalid/v1?x=y", "https://api.example.invalid/#x", "https://api.example.invalid/arbitrary"]) expect(() => loadConnectionAuthorizationConfig({ MISTY_PUBLIC_API_URL: input })).toThrow();
  for (const value of ["//evil.invalid", "/\\evil", "/\nattack"]) expect(connectionAuthorizationInput.safeParse({ return_to: value }).success).toBe(false);
});

it("builds fixed provider URLs with independent state, S256 and capability-specific scopes", () => {
  for (const provider of ["google", "microsoft", "dropbox", "figma", "discord", "instagram"] as const) {
    const access = requestedAccess(provider, []), url = new URL(authorizationUrl(provider, "fixture-client", "https://api.example.invalid/v1/callback", "fixture-state", "fixture-verifier", access.scopes));
    expect(url.protocol).toBe("https:"); expect(url.searchParams.get("state")).toBe("fixture-state"); expect(url.searchParams.get("response_type")).toBe("code");
    if (provider === "instagram") expect(url.searchParams.has("code_challenge")).toBe(false);
    else { expect(url.searchParams.get("code_challenge")).toBe(createHash("sha256").update("fixture-verifier").digest("base64url")); expect(url.searchParams.get("code_challenge_method")).toBe("S256"); }
    expect(url.searchParams.has("client_secret")).toBe(false); expect(url.searchParams.has("code_verifier")).toBe(false);
  }
  expect(requestedAccess("dropbox", []).capabilities).toEqual(["files"]);
  expect(requestedAccess("google", [" MAIL ", "mail"]).capabilities).toEqual(["mail"]);
  for (const provider of ["__proto__", "constructor", "https://attacker.invalid"]) expect(() => connectionProvider(provider)).toThrow();
  expect(() => requestedAccess("google", ["__proto__"])).toThrow();
});

it("removes declined capabilities and does not infer automation from equivalent provider scopes", () => {
  const requested = requestedAccess("google", ["mail", "files"]);
  expect(grantedAccess("google", requested.scopes, requested.capabilities, "https://www.googleapis.com/auth/drive").capabilities).toEqual(["files"]);
  expect(() => grantedAccess("google", requested.scopes, requested.capabilities, "openid profile")).toThrow("provider_permissions_missing");
  expect(() => grantedAccess("google", requested.scopes, requested.capabilities, "")).toThrow("provider_permissions_missing");
  expect(grantedAccess("microsoft", [], ["mail"], "https://graph.microsoft.com/Mail.ReadWrite https://graph.microsoft.com/Mail.Send").capabilities).toEqual(["mail"]);
  expect(grantedAccess("discord", ["identify", "guilds"], ["social_read"]).capabilities).toEqual(["social_read"]);
  expect(grantedAccess("figma", ["file_comments:write"], ["drawings_comments"]).capabilities).toEqual(["drawings_comments"]);
});

it("reads only fixed authenticated identity endpoints and preserves opaque provider IDs", async () => {
  const fetcher = vi.fn<typeof fetch>(), read = createConnectionIdentityReader(fetcher), signal = new AbortController().signal;
  for (const [provider, response, expected] of [
    ["google", { sub: "google-id", email: "test@example.invalid" }, "google-id"],
    ["microsoft", { id: "ms-id", userPrincipalName: "test@example.invalid" }, "ms-id"],
    ["dropbox", { account_id: "dbid:account", email: "test@example.invalid" }, "dbid:account"],
    ["figma", { id: "9999999999999999999", handle: "Designer" }, "9999999999999999999"],
    ["discord", { id: "9999999999999999999", username: "Test" }, "9999999999999999999"],
    ["instagram", { id: "9999999999999999999", username: "Test" }, "9999999999999999999"],
  ] as const) {
    fetcher.mockResolvedValueOnce(Response.json(response)); expect((await read(provider, { access_token: "private-fixture-token" }, signal)).id).toBe(expected);
    const [target, init] = fetcher.mock.calls.at(-1)!, url = new URL(String(target));
    expect(init!.redirect).toBe("error"); expect(init!.method).toBe(provider === "dropbox" ? "POST" : "GET");
    if (provider === "instagram") { expect(url.searchParams.get("access_token")).toBe("private-fixture-token"); expect(new Headers(init!.headers).has("Authorization")).toBe(false); }
    else { expect(new Headers(init!.headers).get("Authorization")).toBe("Bearer private-fixture-token"); expect(url.searchParams.has("access_token")).toBe(false); }
  }
  fetcher.mockResolvedValueOnce(Response.json({ id: 9999999999999999999 })); await expect(read("figma", { access_token: "private-fixture-token" }, signal)).rejects.toThrow("authorization_unavailable");
});

it("bounds identity streams, rejects failed responses, and redacts provider details", async () => {
  const fetcher = vi.fn<typeof fetch>(), read = createConnectionIdentityReader(fetcher), signal = new AbortController().signal;
  const cancel = vi.fn(); let count = 0;
  fetcher.mockResolvedValueOnce(new Response(new ReadableStream({ pull(controller) { count++; controller.enqueue(new Uint8Array(256 * 1024)); }, cancel }), { headers: { "Content-Length": "1" } }));
  await expect(read("google", { access_token: "private" }, signal)).rejects.toThrow("authorization_unavailable"); expect(cancel).toHaveBeenCalled(); expect(count).toBeLessThanOrEqual(6);
  for (const response of [Response.json({ sub: "valid-looking" }, { status: 401 }), new Response("private-provider-error"), Response.json({ sub: "\ninvalid" })]) {
    fetcher.mockResolvedValueOnce(response); await expect(read("google", { access_token: "private" }, signal)).rejects.toThrow("authorization_unavailable");
  }
  fetcher.mockRejectedValueOnce(new Error("https://private-token@example.invalid")); await expect(read("google", { access_token: "private" }, signal)).rejects.toThrow("authorization_unavailable");
});
