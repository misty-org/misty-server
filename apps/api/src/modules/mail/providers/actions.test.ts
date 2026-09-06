import { expect, it, vi } from "vitest";
import { parseMethodResult } from "@misty/contracts";
import type { ConnectionTokenLease } from "../../connections/token-broker.js";
import { createMailActionWriter, type MailWriteGuard } from "./actions.js";

const lease = (provider: "google" | "microsoft"): ConnectionTokenLease => ({ connectionId: "fixture", purpose: "mailWrite", fingerprint: "fixture", accessToken: "private-token", tokenType: "Bearer",
  account: { provider, accountId: "fixture-account", display: "Fixture", status: "active", errorCode: "" } });
const signal = new AbortController().signal;
const guard: MailWriteGuard = (operation) => operation();

it("maps every Gmail label action and encodes opaque IDs once", async () => {
  for (const value of [true, false]) {
    const fetcher = vi.fn<typeof fetch>(async () => Response.json({ id: "%2F+/=" }));
    const result = await createMailActionWriter(lease("google"), guard, fetcher).modifyThread("%2F+/=", { read: value, archived: value, starred: value }, signal);
    expect(() => parseMethodResult("mail.threads.action", result)).not.toThrow();
    expect(result).toEqual({ thread_id: "%2F+/=", added_labels: value ? ["STARRED"] : ["UNREAD", "INBOX"], removed_labels: value ? ["UNREAD", "INBOX"] : ["STARRED"] });
    const [url, options] = fetcher.mock.calls[0]!;
    expect(new URL(String(url)).pathname).toBe("/gmail/v1/users/me/threads/%252F%2B%2F%3D/modify"); expect(options!.method).toBe("POST");
    expect(JSON.parse(options!.body as string)).toEqual({ addLabelIds: result.added_labels, removeLabelIds: result.removed_labels });
    expect(options!.redirect).toBe("error"); expect(new Headers(options!.headers).get("Content-Type")).toBe("application/json");
  }
});

it("gathers and deduplicates Graph pages before guarded sequential patches and moves", async () => {
  const calls: string[] = [], fetcher = vi.fn<typeof fetch>(async (url, options) => {
    const target = new URL(String(url)); calls.push(`${options!.method}:${target.pathname}`);
    if (options!.method === "GET") {
      expect(target.searchParams.get("$filter")).toBe("conversationId eq 'thread''+/='");
      return Response.json({ value: [{ id: target.searchParams.has("$skiptoken") ? "second" : "first+/=", conversationId: "thread'+/=" }],
        ...(target.searchParams.has("$skiptoken") ? {} : { "@odata.nextLink": "?$skiptoken=next" }) });
    }
    expect(new Headers(options!.headers).get("Prefer")).toBe('IdType="ImmutableId"');
    expect(JSON.parse(options!.body as string)).toEqual(options!.method === "PATCH" ? { isRead: false, flag: { flagStatus: "notFlagged" } } : { destinationId: "inbox" });
    return new Response(null, { status: 204 });
  });
  let checks = 0;
  const result = await createMailActionWriter(lease("microsoft"), async (operation) => { checks++; return operation(); }, fetcher).modifyThread("thread'+/=", { read: false, archived: false, starred: false }, signal);
  expect(result).toEqual({ thread_id: "thread'+/=", added_labels: ["UNREAD"], removed_labels: ["ARCHIVED", "STARRED"] }); expect(checks).toBe(4);
  expect(calls).toEqual(["GET:/v1.0/me/messages", "GET:/v1.0/me/messages", "PATCH:/v1.0/me/messages/first%2B%2F%3D", "POST:/v1.0/me/messages/first%2B%2F%3D/move", "PATCH:/v1.0/me/messages/second", "POST:/v1.0/me/messages/second/move"]);
});

it("rejects untrusted or incomplete Graph conversations before any write", async () => {
  for (const page of [
    { value: [{ id: "message", conversationId: "another-thread" }] },
    { value: [{ id: "..", conversationId: "thread" }] },
    { value: [{ id: "message", conversationId: "thread" }], "@odata.nextLink": "https://untrusted.invalid/?$skiptoken=token" },
    { value: Array.from({ length: 501 }, (_, index) => ({ id: String(index), conversationId: "thread" })) },
    { value: [], "@odata.nextLink": "?$skiptoken=repeat" },
  ]) {
    const fetcher = vi.fn<typeof fetch>(async () => Response.json(page));
    await expect(createMailActionWriter(lease("microsoft"), guard, fetcher).modifyThread("thread", { archived: true }, signal)).rejects.toThrow();
    expect(fetcher.mock.calls.every(([, options]) => options!.method === "GET")).toBe(true);
  }
});

it("never retries a failed or ambiguous provider mutation", async () => {
  for (const status of [401, 404, 429, 500]) {
    const fetcher = vi.fn<typeof fetch>(async () => Response.json({ error: { code: "ErrorItemNotFound", message: "private detail" } }, { status }));
    const pending = createMailActionWriter(lease("google"), guard, fetcher).modifyThread("thread", { read: true }, signal);
    if (status === 404) await expect(pending).rejects.toMatchObject({ code: "mail_provider_item_not_found" });
    else await expect(pending).rejects.toThrow();
    expect(fetcher).toHaveBeenCalledTimes(1);
  }
  const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(Response.json({ value: [{ id: "first", conversationId: "thread" }, { id: "second", conversationId: "thread" }] }))
    .mockResolvedValueOnce(new Response(null, { status: 204 })).mockRejectedValueOnce(new Error("connection lost"));
  await expect(createMailActionWriter(lease("microsoft"), guard, fetcher).modifyThread("thread", { read: true, archived: true }, signal)).rejects.toMatchObject({ code: "mail_provider_unavailable" });
  expect(fetcher).toHaveBeenCalledTimes(3);
});

it("stops a multi-message operation when its write grant is revoked", async () => {
  const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(Response.json({ value: [{ id: "first", conversationId: "thread" }, { id: "second", conversationId: "thread" }] }))
    .mockResolvedValue(new Response(null, { status: 204 }));
  let checks = 0;
  const revoked = new Error("revoked");
  await expect(createMailActionWriter(lease("microsoft"), async (operation) => { if (++checks === 2) throw revoked; return operation(); }, fetcher)
    .modifyThread("thread", { read: true }, signal)).rejects.toBe(revoked);
  expect(fetcher).toHaveBeenCalledTimes(2);
});
