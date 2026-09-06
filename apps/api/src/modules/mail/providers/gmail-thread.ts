import { z } from "zod";
import type { MailMessage } from "@misty/contracts";
import { MailError } from "../errors.js";
import { cleanHeader, cleanText, decodeHeader, gmailTime, groupThread, htmlToText, parseAddresses, sortLabels } from "./format.js";

const string = z.string().nullish().transform((value) => value ?? "");
const headers = z.array(z.object({ name: z.string().max(1024).nullish().transform((value) => value ?? ""), value: z.string().max(65536).nullish().transform((value) => value ?? "") })).max(1000).nullish().transform((value) => value ?? []);
const partSchema = z.object({ partId: string, mimeType: string, filename: string, headers,
  body: z.preprocess((value) => value ?? {}, z.object({ attachmentId: string, data: string, size: z.number().int().nonnegative().nullish().transform((value) => value ?? 0) })),
  parts: z.array(z.unknown()).nullish().transform((value) => value ?? []),
});
const messageSchema = z.object({ id: string, threadId: string, labelIds: z.array(z.string()).nullish().transform((value) => value ?? []), snippet: string, internalDate: string, payload: z.unknown().optional() });
export const gmailThreadSchema = z.object({ id: string, snippet: string, messages: z.array(z.unknown()).nullish().transform((value) => value ?? []) });
function parse<T>(schema: z.ZodType<T>, value: unknown): T {
  const result = schema.safeParse(value); if (!result.success) throw new MailError("mail_provider_unavailable"); return result.data;
}
export function normalizeGmailThread(accountId: string, raw: unknown) {
  const thread = parse(gmailThreadSchema, raw); if (!thread.id.trim()) throw new MailError("mail_provider_unavailable");
  return groupThread("gmail", accountId, thread.id, thread.messages.map((message) => normalizeGmailMessage(accountId, thread.id, message)), cleanText(thread.snippet));
}
export function normalizeGmailMessage(accountId: string, fallbackThreadId: string, raw: unknown): MailMessage {
  const source = parse(messageSchema, raw); if (!source.id.trim()) throw new MailError("mail_provider_unavailable");
  if (source.threadId && source.threadId !== fallbackThreadId) throw new MailError("mail_provider_unavailable");
  const root = parse(partSchema, source.payload ?? {}), selected: Record<string, string> = Object.create(null) as Record<string, string>;
  const allowed = new Set(["subject", "from", "to", "cc", "bcc", "reply-to", "date", "message-id"]);
  for (const header of root.headers) {
    const name = header.name.trim().toLowerCase(); if (allowed.has(name) && !selected[name]) selected[name] = cleanHeader(decodeHeader(header.value));
  }
  const plain: string[] = [], html: string[] = [], attachments: MailMessage["attachments"] = [];
  const stack = [{ part: root, depth: 0 }]; let bytes = 0, parts = 0;
  while (stack.length) {
    const { part, depth } = stack.pop()!;
    if (depth > 64 || ++parts > 10000) throw new MailError("mail_response_too_large");
    const mediaType = part.mimeType.trim().toLowerCase().split(";", 1)[0]!.trim();
    const header = (wanted: string) => cleanHeader(part.headers.find((item) => item.name.toLowerCase() === wanted)?.value ?? "");
    const disposition = header("content-disposition").toLowerCase(), contentId = header("content-id").replace(/^[<> \t]+|[<> \t]+$/g, "");
    if (part.filename || part.body.attachmentId || disposition.startsWith("attachment")) {
      attachments.push({ provider: "gmail", provider_id: part.body.attachmentId || `${source.id}:part:${part.partId}`, account_id: accountId,
        message_id: source.id, filename: cleanHeader(part.filename), content_type: mediaType, size: part.body.size, inline: disposition.startsWith("inline") || !!contentId, content_id: contentId });
    } else if (part.body.data && (mediaType === "text/plain" || mediaType === "text/html")) {
      const encoded = part.body.data.replace(/[\r\n]/g, "");
      if (encoded.length > Math.ceil((10 * 1024 * 1024 - bytes) / 3) * 4 + 4) throw new MailError("mail_body_too_large");
      if (!/^[A-Za-z0-9_-]*(?:={1,2})?$/.test(encoded) || encoded.replace(/=+$/, "").length % 4 === 1 || encoded.includes("=") && encoded.length % 4 !== 0) throw new MailError("mail_provider_unavailable");
      const decoded = Buffer.from(encoded, "base64url"); bytes += decoded.byteLength;
      if (bytes > 10 * 1024 * 1024) throw new MailError("mail_body_too_large");
      (mediaType === "text/plain" ? plain : html).push(cleanText(decoded.toString("utf8")));
    }
    if (parts + stack.length + part.parts.length > 10000) throw new MailError("mail_response_too_large");
    for (let index = part.parts.length - 1; index >= 0; index--) stack.push({ part: parse(partSchema, part.parts[index]), depth: depth + 1 });
  }
  const rich = html.join("\n").trim(), labels = sortLabels([...source.labelIds]);
  return { provider: "gmail", provider_id: source.id, account_id: accountId, thread_id: source.threadId || fallbackThreadId,
    rfc822_id: selected["message-id"] ?? "", subject: selected.subject ?? "", from: parseAddresses(selected.from ?? "")[0] ?? { name: "", email: "" },
    to: parseAddresses(selected.to ?? ""), cc: parseAddresses(selected.cc ?? ""), bcc: parseAddresses(selected.bcc ?? ""), reply_to: parseAddresses(selected["reply-to"] ?? ""),
    sent_at: gmailTime(selected.date ?? "", source.internalDate), snippet: cleanText(source.snippet),
    body: { text: plain.length ? plain.join("\n\n").trim() : html.length ? htmlToText(rich) : "", ...(rich ? { html: rich } : {}), had_html: html.length > 0, truncated: false },
    labels, unread: labels.includes("UNREAD"), starred: labels.includes("STARRED"), draft: labels.includes("DRAFT"), attachments };
}
