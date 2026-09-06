import { expect, it, vi } from "vitest";
import type { ConnectionTokenLease } from "../../connections/token-broker.js";
import { createMailReader } from "./reader.js";

const lease: ConnectionTokenLease = { connectionId: "connection_test", purpose: "mailRead", fingerprint: "test",
  account: { provider: "microsoft", accountId: "test-account", display: "Test", status: "active", errorCode: "" }, accessToken: "private-test-token", tokenType: "Bearer" };
const signal = new AbortController().signal;

it("reuses only Graph's opaque skip token and preserves folder normalization across pages", async () => {
  const fetcher = vi.fn<typeof fetch>()
    .mockResolvedValueOnce(Response.json({ value: [{ id: "one+/=", displayName: "Sent &amp; saved\u0000", wellKnownName: "sentitems", totalItemCount: 2, unreadItemCount: 1 }],
      "@odata.nextLink": "https://graph.microsoft.com/ignored-path?$skiptoken=opaque%2B%2F%3D&$select=secrets" }))
    .mockResolvedValueOnce(Response.json({ value: [{ id: "two", displayName: "constructor" }] }));
  const folders = await createMailReader(lease, fetcher).folders(signal);
  expect(folders).toHaveLength(2); expect(folders[0]).toMatchObject({ provider_id: "one+/=", name: "Sent & saved", kind: "sent", system: true, total: 2, unread: 1 });
  expect(folders[1]).toMatchObject({ kind: "custom", system: false, total: 0, unread: 0 });
  const second = new URL(String(fetcher.mock.calls[1]![0]));
  expect(second.pathname).toBe("/v1.0/me/mailFolders"); expect(second.searchParams.get("$skiptoken")).toBe("opaque+/=");
  expect(second.searchParams.get("$select")).toBe("id,displayName,wellKnownName,totalItemCount,unreadItemCount");
});

it("rejects cyclic, excessive and untrusted Graph pagination", async () => {
  for (const nextLink of ["https://evil.invalid/?$skiptoken=x", "//evil.invalid/?$skiptoken=x", "https://user:pass@graph.microsoft.com/?$skiptoken=x", "https://graph.microsoft.com/?$skiptoken=x#fragment", "https://graph.microsoft.com/?missing=x"]) {
    const fetcher = vi.fn<typeof fetch>(async () => Response.json({ value: [], "@odata.nextLink": nextLink }));
    await expect(createMailReader(lease, fetcher).folders(signal)).rejects.toMatchObject({ code: "mail_provider_unavailable" }); expect(fetcher).toHaveBeenCalledTimes(1);
  }
  const cycle = vi.fn<typeof fetch>(async () => Response.json({ value: [], "@odata.nextLink": "?$skiptoken=same" }));
  await expect(createMailReader(lease, cycle).folders(signal)).rejects.toMatchObject({ code: "mail_response_too_large" }); expect(cycle).toHaveBeenCalledTimes(2);
  let page = 0;
  const endless = vi.fn<typeof fetch>(async () => Response.json({ value: [], "@odata.nextLink": `?$skiptoken=${++page}` }));
  await expect(createMailReader(lease, endless).folders(signal)).rejects.toMatchObject({ code: "mail_response_too_large" }); expect(endless).toHaveBeenCalledTimes(20);
  const oversized = vi.fn<typeof fetch>(async () => Response.json({ value: Array.from({ length: 501 }, (_, id) => ({ id: String(id) })) }));
  await expect(createMailReader(lease, oversized).folders(signal)).rejects.toMatchObject({ code: "mail_response_too_large" });
});

it("bounds streamed mail responses and sanitizes provider errors without retrying", async () => {
  const fetcher = vi.fn<typeof fetch>(), reader = createMailReader(lease, fetcher), cancel = vi.fn();
  fetcher.mockResolvedValueOnce(new Response(new ReadableStream({ pull(controller) { controller.enqueue(new Uint8Array(1024 * 1024)); }, cancel })));
  await expect(reader.folders(signal)).rejects.toMatchObject({ code: "mail_response_too_large" }); expect(cancel).toHaveBeenCalled();
  for (const [status, code] of [[401, "mail_provider_authorization_failed"], [403, "mail_provider_authorization_failed"], [404, "mail_provider_item_not_found"], [429, "mail_provider_rate_limited"], [500, "mail_provider_unavailable"]] as const) {
    fetcher.mockResolvedValueOnce(Response.json({ error: { message: "private message must not escape" } }, { status }));
    await expect(createMailReader(lease, fetcher).folders(signal)).rejects.toMatchObject({ message: code, code });
  }
  expect(fetcher).toHaveBeenCalledTimes(6);
  fetcher.mockResolvedValueOnce(Response.json({ value: [{ id: "unsafe", totalItemCount: 9007199254740992 }] }));
  await expect(createMailReader(lease, fetcher).folders(signal)).rejects.toMatchObject({ code: "mail_provider_unavailable" });
});
