import { readFile } from "node:fs/promises";
import { expect, it, vi } from "vitest";
import { parseMethodResult } from "@misty/contracts";
import type { ConnectionTokenLease } from "../../connections/token-broker.js";
import { createMailReader } from "./reader.js";
import { normalizeGmailThread } from "./gmail-thread.js";
import { graphTime, gmailTime, zeroTime } from "./format.js";

const fixtures = JSON.parse(await readFile(new URL("../../../../../../docs/migration/fixtures/mail-threads.json", import.meta.url), "utf8")) as Array<{ name: string; provider: "google" | "microsoft"; threadId: string; raw: unknown; expected: unknown }>;
const lease = (provider: "google" | "microsoft"): ConnectionTokenLease => ({ connectionId: "connection_fixture", purpose: "mailRead", fingerprint: "fixture", account: { provider,
  accountId: "fixture-account", display: "Fixture", status: "active", errorCode: "" }, accessToken: "fixture-only", tokenType: "Bearer" });
const signal = new AbortController().signal, query = { pageSize: 50, pageToken: "", query: "", folderId: "" };

it.each(fixtures)("matches the independent Go HTTP DTO fixture: $name", async (fixture) => {
  const fetcher = vi.fn<typeof fetch>(async () => Response.json(fixture.raw));
  const thread = await createMailReader(lease(fixture.provider), fetcher).thread(fixture.threadId, signal);
  expect(thread).toEqual(fixture.expected); expect(() => parseMethodResult("mail.threads.get", { thread })).not.toThrow();
  const request = new URL(String(fetcher.mock.calls[0]![0]));
  if (fixture.provider === "google") expect(request.pathname).toBe(`/gmail/v1/users/me/threads/${encodeURIComponent(fixture.threadId)}`);
  else expect(request.searchParams.get("$filter")).toBe(`conversationId eq '${fixture.threadId}'`);
});

it("bounds Gmail metadata fanout and preserves order and query filters", async () => {
  let resolve!: () => void; const release = new Promise<void>((done) => { resolve = done; });
  let started!: () => void; const allStarted = new Promise<void>((done) => { started = done; });
  let active = 0, peak = 0;
  const fetcher = vi.fn<typeof fetch>(async (url) => {
    const target = new URL(String(url));
    if (target.pathname.endsWith("/threads")) return Response.json({ threads: Array.from({ length: 25 }, (_, id) => ({ id: `thread_${id}` })), nextPageToken: "next+/=", resultSizeEstimate: 99 });
    active++; peak = Math.max(peak, active); if (active === 10) started(); await release; active--;
    expect(target.searchParams.getAll("metadataHeaders")).toEqual(["Subject", "From", "To", "Cc", "Date"]);
    const id = decodeURIComponent(target.pathname.split("/").at(-1)!);
    return Response.json({ id, messages: [{ id: `${id}_message`, payload: { headers: [{ name: "Subject", value: id }] } }] });
  });
  const pending = createMailReader(lease("google"), fetcher).threads({ ...query, pageSize: 25, pageToken: "page+/=", query: "from:a@example.invalid", folderId: "label+/=" }, signal);
  await allStarted; expect(peak).toBe(10); resolve(); const page = await pending;
  expect(page.threads.map((thread) => thread.subject)).toEqual(Array.from({ length: 25 }, (_, id) => `thread_${id}`));
  expect(page.next_page_token).toBe("next+/="); expect(page.estimated_total).toBe(99);
  const target = new URL(String(fetcher.mock.calls[0]![0])); expect(target.searchParams.get("pageToken")).toBe("page+/="); expect(target.searchParams.get("labelIds")).toBe("label+/=");
});

it("uses a Gmail snippet fallback for a missing preview but propagates authorization and throttling", async () => {
  for (const status of [404, 401, 429]) {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(Response.json({ threads: [{ id: "thread", snippet: "Fallback &amp; text" }] }))
      .mockResolvedValueOnce(Response.json({ error: { message: "private provider detail" } }, { status }));
    const pending = createMailReader(lease("google"), fetcher).threads(query, signal);
    if (status === 404) { const result = await pending; expect(result.threads[0]).toMatchObject({ provider_id: "thread", snippet: "Fallback & text", messages: [] }); }
    else await expect(pending).rejects.toMatchObject({ code: status === 401 ? "mail_provider_authorization_failed" : "mail_provider_rate_limited" });
  }
});

it("cancels sibling Gmail requests after a fatal preview failure", async () => {
  let release!: () => void;
  const started = new Promise<void>((resolve) => { release = resolve; });
  let active = 0, cancelled = 0;
  const fetcher = vi.fn<typeof fetch>(async (url, options) => {
    const path = new URL(String(url)).pathname;
    if (path.endsWith("/threads")) return Response.json({ threads: Array.from({ length: 25 }, (_, index) => ({ id: `thread_${index}` })) });
    active++; if (active === 10) release();
    if (path.endsWith("/thread_0")) { await started; return Response.json({}, { status: 401 }); }
    return new Promise<Response>((_resolve, reject) => {
      options!.signal!.addEventListener("abort", () => { cancelled++; reject(new Error("aborted")); }, { once: true });
    });
  });
  await expect(createMailReader(lease("google"), fetcher).threads(query, signal)).rejects.toMatchObject({ code: "mail_provider_authorization_failed" });
  expect(active).toBe(10); expect(cancelled).toBe(9); expect(fetcher).toHaveBeenCalledTimes(11);
});

it("enforces one streamed response budget across Graph conversation pages", async () => {
  const fetcher = vi.fn<typeof fetch>(async () => Response.json({ value: [], padding: "x".repeat(9 * 1024 * 1024),
    "@odata.nextLink": "https://graph.microsoft.com/v1.0/me/messages?$skiptoken=next" }));
  await expect(createMailReader(lease("microsoft"), fetcher).thread("thread", signal)).rejects.toMatchObject({ code: "mail_response_too_large" });
  expect(fetcher).toHaveBeenCalledTimes(2);
});

it("groups Graph messages and safely encodes search, folder and conversation identifiers", async () => {
  const fixture = fixtures.find((entry) => entry.name === "graph-body-and-precision")!;
  const fetcher = vi.fn<typeof fetch>(async () => Response.json(fixture.raw));
  const result = await createMailReader(lease("microsoft"), fetcher).threads({ ...query, query: 'a"b', folderId: "folder'quote", pageToken: "opaque+/=" }, signal);
  expect(() => parseMethodResult("mail.threads.list", result)).not.toThrow(); expect(result.threads).toHaveLength(2); expect(result.estimated_total).toBe(2);
  expect(result.threads[1]).toEqual(fixture.expected);
  const target = new URL(String(fetcher.mock.calls[0]![0])); expect(target.searchParams.get("$search")).toBe('"a\\"b"');
  expect(target.searchParams.get("$filter")).toBe("(parentFolderId eq 'folder''quote')"); expect(target.searchParams.has("$orderby")).toBe(false);
  expect(target.searchParams.get("$skiptoken")).toBe("opaque+/=");
  await expect(createMailReader(lease("microsoft"), fetcher).thread("wrong-conversation", signal)).rejects.toMatchObject({ code: "mail_provider_item_not_found" });
});

it("retries Graph expansion only for an initial 400 and bounds conversation pagination", async () => {
  const fixture = fixtures.find((entry) => entry.name === "graph-body-and-precision")!;
  const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(Response.json({ error: { message: "unsupported expand" } }, { status: 400 })).mockResolvedValueOnce(Response.json(fixture.raw));
  expect(await createMailReader(lease("microsoft"), fetcher).thread(fixture.threadId, signal)).toEqual(fixture.expected);
  expect(new URL(String(fetcher.mock.calls[1]![0])).searchParams.has("$expand")).toBe(false);
  const denied = vi.fn<typeof fetch>(async () => Response.json({}, { status: 403 }));
  await expect(createMailReader(lease("microsoft"), denied).thread("id", signal)).rejects.toMatchObject({ code: "mail_provider_authorization_failed" }); expect(denied).toHaveBeenCalledTimes(1);
  const cycle = vi.fn<typeof fetch>(async () => Response.json({ value: [], "@odata.nextLink": "?$skiptoken=same" }));
  await expect(createMailReader(lease("microsoft"), cycle).thread("id", signal)).rejects.toMatchObject({ code: "mail_response_too_large" }); expect(cycle).toHaveBeenCalledTimes(2);
});

it("rejects malformed or excessive MIME and preserves safe timestamp fallbacks", () => {
  const source = (payload: unknown) => ({ id: "thread", messages: [{ id: "message", payload }] });
  expect(() => normalizeGmailThread("account", source({ mimeType: "text/plain", body: { data: "not+url/base64" } }))).toThrow();
  expect(() => normalizeGmailThread("account", source({ mimeType: "text/plain", body: { data: Buffer.alloc(10 * 1024 * 1024 + 1).toString("base64url") } }))).toThrowError("mail_body_too_large");
  let deep: unknown = {}; for (let i = 0; i < 66; i++) deep = { parts: [deep] };
  expect(() => normalizeGmailThread("account", source(deep))).toThrowError("mail_response_too_large");
  expect(gmailTime("31 Feb 2026 12:00:00 +0000", "0")).toBe("1970-01-01T00:00:00Z");
  expect(gmailTime("not a date", "invalid")).toBe(zeroTime);
  expect(graphTime("2026-02-31T12:00:00Z", "2026-09-05T13:00:00.000000001+01:00")).toBe("2026-09-05T12:00:00.000000001Z");
});
