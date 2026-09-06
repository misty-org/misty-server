import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { expect, it, vi } from "vitest";
import { createOAuthTokenClient } from "./oauth-token.js";
import { loadConnectionOAuthClients } from "./config.js";

it("uses fixed token endpoints and private provider client authentication", async () => {
  const fetcher = vi.fn<typeof fetch>(async () => Response.json({ access_token: "new-access", refresh_token: "new-refresh", expires_in: 3600, unknown_secret: "discard" }));
  const clients = loadConnectionOAuthClients({ GOOGLE_CLIENT_ID: "google-id", GOOGLE_CLIENT_SECRET: "google-secret", FIGMA_CLIENT_ID: "figma-id", FIGMA_CLIENT_SECRET: "figma-secret", INSTAGRAM_CLIENT_ID: "ig-id", INSTAGRAM_CLIENT_SECRET: "ig-secret", MICROSOFT_CLIENT_ID: "incomplete" });
  const client = createOAuthTokenClient(clients, fetcher), signal = new AbortController().signal;
  expect(await client.refresh("google", "old+/=refresh", signal)).toEqual({ access_token: "new-access", refresh_token: "new-refresh", expires_in: 3600 });
  expect(fetcher.mock.calls[0]![0]).toBe("https://oauth2.googleapis.com/token");
  const google = fetcher.mock.calls[0]![1]!; expect(google.redirect).toBe("error"); expect(google.method).toBe("POST");
  expect(Object.fromEntries(new URLSearchParams(String(google.body)))).toEqual({ grant_type: "refresh_token", refresh_token: "old+/=refresh", client_id: "google-id", client_secret: "google-secret" });
  await client.exchangeCode("figma", "code", "verifier", "https://api.example.invalid/oauth/connections/figma/callback", signal);
  const figma = fetcher.mock.calls[1]![1]!;
  expect(new Headers(figma.headers).get("Authorization")).toBe(`Basic ${Buffer.from("figma-id:figma-secret").toString("base64")}`);
  expect(new URLSearchParams(String(figma.body)).has("client_secret")).toBe(false); expect(new URLSearchParams(String(figma.body)).get("code_verifier")).toBe("verifier");
  await client.exchangeCode("instagram", "code", "unused", "https://api.example.invalid/callback", signal);
  expect(new URLSearchParams(String(fetcher.mock.calls[2]![1]!.body)).has("code_verifier")).toBe(false);
  await expect(client.refresh("microsoft", "old-refresh", signal)).rejects.toMatchObject({ code: "not_configured" });
  expect(fetcher).toHaveBeenCalledTimes(3);
  await client.refresh("figma", "old-figma-refresh", signal);
  expect(fetcher.mock.calls[3]![0]).toBe("https://api.figma.com/v1/oauth/refresh");
  expect(new Headers(fetcher.mock.calls[3]![1]!.headers).get("Authorization")).toBe(`Basic ${Buffer.from("figma-id:figma-secret").toString("base64")}`);
});

it("bounds streamed token responses and exposes only sanitized failure categories", async () => {
  const fetcher = vi.fn<typeof fetch>(), client = createOAuthTokenClient({ google: { clientId: "id", clientSecret: "secret" } }, fetcher), signal = new AbortController().signal;
  const cancel = vi.fn(); let chunks = 0;
  fetcher.mockResolvedValueOnce(new Response(new ReadableStream({ pull(controller) { controller.enqueue(new Uint8Array(256 * 1024)); chunks++; }, cancel }), { headers: { "Content-Length": "1" } }));
  await expect(client.refresh("google", "old-refresh", signal)).rejects.toMatchObject({ message: "response_too_large" }); expect(cancel).toHaveBeenCalled(); expect(chunks).toBeLessThanOrEqual(6);
  for (const [response, code] of [
    [Response.json({ error: "invalid_grant", error_description: "private-secret" }, { status: 400 }), "reauthorization_required"],
    [Response.json({ error: "private-secret" }, { status: 503 }), "unavailable"],
    [new Response("private-secret", { status: 200 }), "invalid_response"],
    [Response.json({ access_token: "header\r\ninjection" }), "invalid_response"],
    [Response.json({ access_token: "token", expires_in: -1 }), "invalid_response"],
  ] as const) {
    fetcher.mockResolvedValueOnce(response); await expect(client.refresh("google", "old-refresh", signal)).rejects.toMatchObject({ message: code, code });
  }
  fetcher.mockRejectedValue(new Error("private-secret in URL"));
  await expect(client.refresh("google", "old-refresh", signal)).rejects.toMatchObject({ message: "unavailable" });
});

it("uses explicit Instagram permission evidence, including an empty grant", async () => {
  const fetcher = vi.fn<typeof fetch>(), client = createOAuthTokenClient({ instagram: { clientId: "id", clientSecret: "secret" } }, fetcher), signal = new AbortController().signal;
  for (const permissions of [["instagram_business_basic"], []]) {
    fetcher.mockResolvedValueOnce(Response.json({ access_token: "fixture", permissions }));
    expect((await client.exchangeCode("instagram", "code", "unused", "https://api.example.invalid/callback", signal)).scope).toBe(permissions.join(" "));
  }
  fetcher.mockResolvedValueOnce(Response.json({ access_token: "fixture", permissions: "malformed" }));
  await expect(client.exchangeCode("instagram", "code", "unused", "https://api.example.invalid/callback", signal)).rejects.toThrow("invalid_response");
});

it("aborts a real stalled HTTP response when the caller cancels", async () => {
  const controller = new AbortController();
  const server = createServer((_request, response) => { response.writeHead(200, { "Content-Type": "application/json" }); response.write('{"access_token":"'); controller.abort(); });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  try {
    const origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    // Only this test redirects the fixed transport to a local fake provider.
    const client = createOAuthTokenClient({ google: { clientId: "test-id", clientSecret: "test-secret" } }, (_url, init) => fetch(origin, init));
    await expect(client.refresh("google", "fake-refresh", controller.signal)).rejects.toMatchObject({ code: "unavailable" });
  } finally { server.closeAllConnections(); await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve())); }
});
