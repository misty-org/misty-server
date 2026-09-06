import { expect, it, vi } from "vitest";
import { simpleParser } from "mailparser";
import { parseMethodResult, type MailDraftInput } from "@misty/contracts";
import type { ConnectionTokenLease } from "../../connections/token-broker.js";
import { prepareDraft } from "./draft-input.js";
import { createGmailDraftWriter, renderGmailDraft } from "./gmail-drafts.js";
import { uploadGraphAttachment } from "./graph-upload.js";
import { createGraphDraftWriter } from "./graph-drafts.js";
import type { MailWriteGuard } from "./actions.js";

const input: MailDraftInput = { connection_id: "connection_fixture", to: [{ name: "雪, Friend", email: "friend@example.invalid" }],
  bcc: [{ email: "hidden@example.invalid" }], reply_to: [{ email: "reply@example.invalid" }], subject: "Résumé ✓", text: "Hello 雪\n\nLine two\n" };
const lease: ConnectionTokenLease = { connectionId: input.connection_id, purpose: "mailWrite", fingerprint: "fixture", accessToken: "private-token", tokenType: "Bearer",
  account: { provider: "google", accountId: "fixture-account", display: "Fixture", status: "active", errorCode: "" } };
const signal = new AbortController().signal, guard: MailWriteGuard = (operation) => operation();
const uploadUrl = "https://outlook.office.com/api/v2.0/Users('fixture')/Messages('message%2B%2F%3D')/AttachmentSessions('session')?authtoken=private-upload-token";
const session = () => ({ uploadUrl, expirationDateTime: new Date(Date.now() + 600000).toISOString(), nextExpectedRanges: ["0-"] });

it("renders Unicode MIME, Bcc, reply headers and binary attachments without loading paths or URLs", async () => {
  const bytes = Buffer.from([0, 255, 128, 10, 13]), data = prepareDraft({ ...input, attachments: [
    { filename: "雪.png", content_type: "image/png", data: bytes.toString("base64"), inline: true, content_id: "image@example.invalid" },
    { filename: "https://private.invalid/secrets", content_type: "not a MIME type", data: "", inline: false },
  ] });
  const parsed = await simpleParser(Buffer.from(await renderGmailDraft(data, { inReplyTo: "<parent@example.invalid>", references: ["<older@example.invalid>", "<parent@example.invalid>"] }), "base64url"));
  expect(parsed.subject).toBe(input.subject); expect(parsed.text).toBe(input.text); expect(parsed.to).toMatchObject({ value: [{ name: "雪, Friend", address: "friend@example.invalid" }] });
  expect(parsed.bcc).toMatchObject({ value: [{ address: "hidden@example.invalid" }] }); expect(parsed.replyTo).toMatchObject({ value: [{ address: "reply@example.invalid" }] });
  expect(parsed.inReplyTo).toBe("<parent@example.invalid>"); expect(parsed.references).toEqual(["<older@example.invalid>", "<parent@example.invalid>"]);
  expect(parsed.attachments[0]).toMatchObject({ filename: "雪.png", content: bytes, contentDisposition: "inline", contentId: "<image@example.invalid>" });
  expect(parsed.attachments[1]).toMatchObject({ contentType: "application/octet-stream", content: Buffer.alloc(0) });
});

it("rejects header injection, invalid recipients, noncanonical base64 and oversized decoded drafts", () => {
  for (const raw of [
    { ...input, subject: "hello\r\nBcc: injected@example.invalid" },
    { ...input, to: [{ email: "Someone <person@example.invalid>" }] },
    { ...input, to: [{ email: "one@example.invalid,two@example.invalid" }] },
    { ...input, attachments: [{ filename: "file", content_type: "text/plain", data: "YR==", inline: false }] },
    { ...input, text: "雪".repeat(Math.ceil(10 * 1024 * 1024 / 3)) },
  ]) expect(() => prepareDraft(raw)).toThrowError("mail_invalid_request");
});

it("creates Gmail replies with provider message references and explicit recipients", async () => {
  let parsed: Awaited<ReturnType<typeof simpleParser>> | undefined;
  const fetcher = vi.fn<typeof fetch>(async (url, options) => {
    if (options!.method === "GET") {
      expect(new URL(String(url)).searchParams.getAll("metadataHeaders")).toEqual(["Message-ID", "References", "Subject"]);
      return Response.json({ id: "thread+/=", messages: [{ id: "parent", threadId: "thread+/=", internalDate: "1", payload: { headers: [
        { name: "Message-ID", value: "<parent@example.invalid>" }, { name: "References", value: "<older@example.invalid>" }, { name: "Subject", value: input.subject },
      ] } }] });
    }
    const body = JSON.parse(options!.body as string); expect(body.message.threadId).toBe("thread+/="); parsed = await simpleParser(Buffer.from(body.message.raw, "base64url"));
    return Response.json({ id: "draft+/=", message: { id: "message", threadId: "thread+/=", labelIds: ["DRAFT"] } });
  });
  const draft = await createGmailDraftWriter(lease, guard, fetcher).createDraft(prepareDraft({ ...input, subject: `Re: ${input.subject}`, thread_id: "thread+/=" }), signal);
  expect(() => parseMethodResult("mail.drafts.create", { draft })).not.toThrow(); expect(parsed!.inReplyTo).toBe("<parent@example.invalid>");
  expect(parsed!.subject).toBe(`Re: ${input.subject}`); expect(fetcher).toHaveBeenCalledTimes(2);
});

it("updates an existing standalone Gmail draft and sends only an existing draft ID", async () => {
  const fetcher = vi.fn<typeof fetch>(async (url, options) => {
    const path = new URL(String(url)).pathname;
    if (path.endsWith("/threads/thread")) return Response.json({ id: "thread", messages: [{ id: "message", threadId: "thread", labelIds: ["DRAFT"] }] });
    if (path.endsWith("/send")) { expect(JSON.parse(options!.body as string)).toEqual({ id: "%2F+/=" }); return Response.json({ id: "sent", threadId: "thread", labelIds: ["SENT"] }); }
    if (options!.method === "PUT") expect(JSON.parse(options!.body as string)).toMatchObject({ id: "%2F+/=", message: { threadId: "thread" } });
    return Response.json({ id: "%2F+/=", message: { id: "message", threadId: "thread", labelIds: ["DRAFT"] } });
  });
  const writer = createGmailDraftWriter(lease, guard, fetcher), draft = await writer.updateDraft("%2F+/=", prepareDraft(input), signal);
  expect(() => parseMethodResult("mail.drafts.update", { draft })).not.toThrow(); const message = await writer.sendDraft("%2F+/=", signal);
  expect(() => parseMethodResult("mail.drafts.send", { message })).not.toThrow(); expect(message.draft).toBe(false);
  expect(fetcher.mock.calls.filter(([, options]) => options!.method === "POST")).toHaveLength(1);
});

it("does not write or send after failed Gmail identity checks, and never retries a lost send response", async () => {
  const invalid = vi.fn<typeof fetch>(async () => Response.json({ id: "wrong", message: { id: "message", threadId: "thread", labelIds: ["DRAFT"] } }));
  await expect(createGmailDraftWriter(lease, guard, invalid).sendDraft("draft", signal)).rejects.toThrow(); expect(invalid).toHaveBeenCalledTimes(1);
  const lost = vi.fn<typeof fetch>().mockResolvedValueOnce(Response.json({ id: "draft", message: { id: "message", threadId: "thread", labelIds: ["DRAFT"] } })).mockRejectedValueOnce(new Error("private provider failure"));
  await expect(createGmailDraftWriter(lease, guard, lost).sendDraft("draft", signal)).rejects.toThrowError("mail_provider_unavailable"); expect(lost).toHaveBeenCalledTimes(2);
});

it("uploads Outlook attachment bytes in bounded ordered ranges without forwarding OAuth credentials", async () => {
  const data = Buffer.alloc(5 * 1024 * 1024 + 3, 123), ranges: string[] = [], parts: Buffer[] = [];
  const fetcher = vi.fn<typeof fetch>(async (url, options) => {
    expect(url).toBe(uploadUrl); expect(options!.redirect).toBe("error"); expect(options!.method).toBe("PUT");
    const headers = new Headers(options!.headers); expect(headers.has("Authorization")).toBe(false);
    ranges.push(headers.get("Content-Range")!); parts.push(Buffer.from(options!.body as Uint8Array));
    const end = parts.reduce((total, part) => total + part.length, 0); expect(headers.get("Content-Length")).toBe(String(parts.at(-1)!.length));
    return end === data.length ? new Response(null, { status: 201 }) : Response.json({ nextExpectedRanges: [String(end)] });
  });
  await uploadGraphAttachment(session(), data, signal, guard, fetcher);
  expect(Buffer.concat(parts).equals(data)).toBe(true); expect(ranges).toEqual(["bytes 0-2097151/5242883", "bytes 2097152-4194303/5242883", "bytes 4194304-5242882/5242883"]);
});

it("rejects unsafe or expired Outlook upload sessions before dispatch", async () => {
  for (const raw of [ { ...session(), uploadUrl: "https://evil.invalid/file?authtoken=secret" }, { ...session(), uploadUrl: uploadUrl.replace("outlook.office.com", "outlook.office.com.evil.invalid") },
    { ...session(), uploadUrl: uploadUrl + "#fragment" }, { ...session(), uploadUrl: "https://outlook.office.com/another-api?authtoken=secret" },
    { ...session(), expirationDateTime: "2000-01-01T00:00:00Z" }, { ...session(), nextExpectedRanges: ["12-"] } ]) {
    const fetcher = vi.fn<typeof fetch>(); await expect(uploadGraphAttachment(raw, Buffer.alloc(4 * 1024 * 1024), signal, guard, fetcher)).rejects.toThrowError("mail_provider_unavailable"); expect(fetcher).not.toHaveBeenCalled();
  }
});

it("stops Outlook uploads on unexpected ranges, early completion, revocation or failure without retries", async () => {
  for (const response of [Response.json({ nextExpectedRanges: ["0-"] }), new Response(null, { status: 201 }), new Response(null, { status: 401 }), new Response("x".repeat(256 * 1024 + 1))]) {
    const fetcher = vi.fn<typeof fetch>(async () => response);
    await expect(uploadGraphAttachment(session(), Buffer.alloc(4 * 1024 * 1024), signal, guard, fetcher)).rejects.toThrow(); expect(fetcher).toHaveBeenCalledTimes(1);
  }
  const fetcher = vi.fn<typeof fetch>(async () => Response.json({ nextExpectedRanges: ["2097152-"] })); let checks = 0;
  await expect(uploadGraphAttachment(session(), Buffer.alloc(4 * 1024 * 1024), signal, async (operation) => { if (++checks === 2) throw new Error("revoked"); return operation(); }, fetcher)).rejects.toThrowError("revoked");
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("creates Graph drafts with small attachments and large upload sessions before returning metadata", async () => {
  const data = Buffer.alloc(4 * 1024 * 1024, 17), graphLease: ConnectionTokenLease = { ...lease, account: { ...lease.account, provider: "microsoft" } };
  const created = vi.fn(async (_id: string) => {}), uploaded: Buffer[] = [], calls: string[] = [];
  const fetcher = vi.fn<typeof fetch>(async (url, options) => {
    const target = new URL(String(url)), method = options!.method; calls.push(`${method}:${target.pathname}`);
    if (target.origin === "https://outlook.office.com") {
      uploaded.push(Buffer.from(options!.body as Uint8Array));
      return uploaded.length === 1 ? Response.json({ nextExpectedRanges: ["2097152"] }) : new Response(null, { status: 201 });
    }
    if (target.pathname.endsWith("/createUploadSession")) {
      expect(created).toHaveBeenCalledWith("draft+/=");
      expect(JSON.parse(options!.body as string)).toEqual({ AttachmentItem: { attachmentType: "file", name: "large.bin", size: data.length, contentType: "application/octet-stream", isInline: true, contentId: "large@example.invalid" } });
      return Response.json(session(), { status: 201 });
    }
    if (target.pathname.endsWith("/attachments")) {
      expect(created).toHaveBeenCalledWith("draft+/=");
      expect(JSON.parse(options!.body as string)).toMatchObject({ contentBytes: "AQI=", name: "small.bin", "@odata.type": "#microsoft.graph.fileAttachment" });
      return Response.json({ id: "small" }, { status: 201 });
    }
    if (method === "POST") expect(JSON.parse(options!.body as string)).toMatchObject({ bccRecipients: [{ emailAddress: { address: "hidden@example.invalid" } }], ccRecipients: [], body: { contentType: "Text", content: input.text } });
    return Response.json({ id: "draft+/=", conversationId: "thread", isDraft: true, subject: input.subject, attachments: method === "GET" ? [{ id: "large", name: "large.bin", size: data.length }] : [] });
  });
  const draft = await createGraphDraftWriter(graphLease, guard, fetcher, created).createDraft(prepareDraft({ ...input, attachments: [
    { filename: "small.bin", content_type: "application/octet-stream", data: "AQI=", inline: false },
    { filename: "large.bin", content_type: "application/octet-stream", data: data.toString("base64"), inline: true, content_id: "large@example.invalid" },
  ] }), signal);
  expect(() => parseMethodResult("mail.drafts.create", { draft })).not.toThrow(); expect(draft.message.attachments[0]!.size).toBe(data.length);
  expect(Buffer.concat(uploaded).equals(data)).toBe(true); expect(calls.at(-1)).toBe("GET:/v1.0/me/messages/draft%2B%2F%3D");
  expect(calls.some((call) => call.includes("/send"))).toBe(false);
});

it("uses Graph createReply for the latest existing message and applies only requested draft content", async () => {
  const graphLease: ConnectionTokenLease = { ...lease, account: { ...lease.account, provider: "microsoft" } }, calls: string[] = [];
  const fetcher = vi.fn<typeof fetch>(async (url, options) => {
    const target = new URL(String(url)); calls.push(`${options!.method}:${target.pathname}`);
    if (options!.method === "GET" && target.pathname.endsWith("/messages")) return Response.json({ value: [
      { id: "old", conversationId: "thread", subject: input.subject, sentDateTime: "2026-01-01T00:00:00.000000001Z" },
      { id: "latest+/=", conversationId: "thread", subject: input.subject, sentDateTime: "2026-01-01T00:00:00.000000002Z" },
      { id: "existing-draft", conversationId: "thread", isDraft: true, subject: input.subject, sentDateTime: "2026-09-01T00:00:00Z" },
    ] });
    if (options!.method === "PATCH") expect(JSON.parse(options!.body as string)).toMatchObject({ body: { content: input.text }, toRecipients: [{ emailAddress: { address: "friend@example.invalid" } }] });
    return Response.json({ id: "reply", conversationId: "thread", isDraft: true, subject: input.subject });
  });
  await createGraphDraftWriter(graphLease, guard, fetcher).createDraft(prepareDraft({ ...input, thread_id: "thread" }), signal);
  expect(calls).toEqual(["GET:/v1.0/me/messages", "POST:/v1.0/me/messages/latest%2B%2F%3D/createReply", "PATCH:/v1.0/me/messages/reply", "GET:/v1.0/me/messages/reply"]);
});

it("snapshots all Graph attachment pages before replacement and explicitly clears removed recipients", async () => {
  const graphLease: ConnectionTokenLease = { ...lease, account: { ...lease.account, provider: "microsoft" } }, calls: string[] = [];
  const fetcher = vi.fn<typeof fetch>(async (url, options) => {
    const target = new URL(String(url)); calls.push(`${options!.method}:${target.pathname}`);
    if (target.pathname.endsWith("/attachments") && options!.method === "GET") return Response.json({ value: [{ id: target.searchParams.has("$skiptoken") ? "second" : "first+/=" }],
      ...(target.searchParams.has("$skiptoken") ? {} : { "@odata.nextLink": "?$skiptoken=next" }) });
    if (options!.method === "PATCH") expect(JSON.parse(options!.body as string)).toMatchObject({ ccRecipients: [], bccRecipients: [], replyTo: [] });
    return options!.method === "DELETE" ? new Response(null, { status: 204 }) : Response.json({ id: "draft+/=", conversationId: "thread", isDraft: true });
  });
  const draft = await createGraphDraftWriter(graphLease, guard, fetcher).updateDraft("draft+/=", prepareDraft({ ...input, bcc: [], reply_to: [] }), signal);
  expect(() => parseMethodResult("mail.drafts.update", { draft })).not.toThrow();
  expect(calls).toEqual(["GET:/v1.0/me/messages/draft%2B%2F%3D", "GET:/v1.0/me/messages/draft%2B%2F%3D/attachments", "GET:/v1.0/me/messages/draft%2B%2F%3D/attachments", "PATCH:/v1.0/me/messages/draft%2B%2F%3D", "DELETE:/v1.0/me/messages/draft%2B%2F%3D/attachments/first%2B%2F%3D", "DELETE:/v1.0/me/messages/draft%2B%2F%3D/attachments/second", "GET:/v1.0/me/messages/draft%2B%2F%3D"]);
});

it("validates a Graph draft before send, sends no body and rejects non-drafts without a write", async () => {
  const graphLease: ConnectionTokenLease = { ...lease, account: { ...lease.account, provider: "microsoft" } };
  for (const isDraft of [false, true]) {
    const fetcher = vi.fn<typeof fetch>(async (_url, options) => {
      if (options!.method === "GET") return Response.json({ id: "draft", conversationId: "thread", isDraft, subject: input.subject });
      expect(options!.method).toBe("POST"); expect(options!.body).toBeUndefined(); return new Response(null, { status: 202 });
    });
    const pending = createGraphDraftWriter(graphLease, guard, fetcher).sendDraft("draft", signal);
    if (!isDraft) { await expect(pending).rejects.toThrowError("mail_invalid_request"); expect(fetcher).toHaveBeenCalledTimes(1); }
    else { const message = await pending; expect(() => parseMethodResult("mail.drafts.send", { message })).not.toThrow(); expect(message.draft).toBe(false); expect(message.labels).not.toContain("DRAFT"); }
  }
});

it("stops Graph draft creation when durable draft identity cannot be recorded", async () => {
  const graphLease: ConnectionTokenLease = { ...lease, account: { ...lease.account, provider: "microsoft" } }, failed = new Error("audit unavailable");
  const fetcher = vi.fn<typeof fetch>(async () => Response.json({ id: "draft", conversationId: "thread", isDraft: true }));
  await expect(createGraphDraftWriter(graphLease, guard, fetcher, async () => { throw failed; }).createDraft(prepareDraft({ ...input, attachments: [{ filename: "file", content_type: "text/plain", data: "AQI=", inline: false }] }), signal)).rejects.toBe(failed);
  expect(fetcher).toHaveBeenCalledTimes(1);
});
