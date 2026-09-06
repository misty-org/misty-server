import MailComposer from "nodemailer/lib/mail-composer/index.js";
import { z } from "zod";
import type { MailDraft, MailMessage } from "@misty/contracts";
import type { ConnectionTokenLease } from "../../connections/token-broker.js";
import { MailError } from "../errors.js";
import { normalizeGmailMessage } from "./gmail-thread.js";
import { decodeHeader } from "./format.js";
import { createMailTransport } from "./transport.js";
import type { MailWriteGuard } from "./actions.js";
import type { PreparedDraft } from "./draft-input.js";

const idSchema = z.string().regex(/^[\x21-\x7e]{1,320}$/).refine((id) => id !== "." && id !== "..");
const draftSchema = z.object({ id: idSchema, message: z.object({ id: idSchema, threadId: idSchema, labelIds: z.array(z.string()).optional() }).passthrough() });
const replySchema = z.object({ id: idSchema, messages: z.array(z.object({ id: idSchema, threadId: idSchema.optional(), internalDate: z.string().regex(/^\d{1,16}$/).optional(),
  labelIds: z.array(z.string()).optional(), payload: z.object({ headers: z.array(z.object({ name: z.string().max(1024), value: z.string().max(65536) })).max(1000) }).optional() })).max(500) });
const messageId = /^<[^<>@\s\x00-\x1f\x7f]+@[^<>@\s\x00-\x1f\x7f]+>$/;
const subjectKey = (subject: string) => decodeHeader(subject).trim().replace(/^(?:re:\s*)+/i, "");
function parse<T>(schema: z.ZodType<T>, raw: unknown): T { const result = schema.safeParse(raw); if (!result.success) throw new MailError("mail_provider_unavailable"); return result.data; }

export async function renderGmailDraft(input: PreparedDraft, reply: { inReplyTo?: string; references?: string[] } = {}) {
  const message = new MailComposer({
    to: input.to, cc: input.cc, bcc: input.bcc, replyTo: input.replyTo, subject: input.subject, text: input.text,
    textEncoding: "base64", disableFileAccess: true, disableUrlAccess: true,
    ...reply,
    attachments: input.attachments.map((file) => ({ filename: file.filename, contentType: file.contentType, content: file.data,
      contentDisposition: file.inline ? "inline" : "attachment", ...(file.contentId ? { cid: file.contentId } : {}) })),
  }).compile();
  // Gmail receives the whole MIME message, including envelope recipients.
  message.keepBcc = true;
  const raw = await message.build();
  if (raw.byteLength > 20 * 1024 * 1024) throw new MailError("mail_body_too_large");
  return raw.toString("base64url");
}

export function createGmailDraftWriter(lease: ConnectionTokenLease, guard: MailWriteGuard, fetcher: typeof fetch = fetch, created: (id: string) => Promise<void> = async () => {}) {
  const request = createMailTransport(lease, fetcher);
  const normalize = (raw: unknown, expectedId?: string): MailDraft => {
    const source = parse(draftSchema, raw);
    if (expectedId && source.id !== expectedId) throw new MailError("mail_provider_unavailable");
    return { provider: "gmail", provider_id: source.id, account_id: lease.account.accountId, thread_id: source.message.threadId,
      message: normalizeGmailMessage(lease.account.accountId, source.message.threadId, source.message) };
  };
  const existing = async (id: string, signal: AbortSignal) => {
    if (!idSchema.safeParse(id).success) throw new MailError("mail_invalid_request");
    const result = parse(draftSchema, await request(["users", "me", "drafts", id], new URLSearchParams({ format: "minimal" }), signal));
    if (result.id !== id || !result.message.labelIds?.includes("DRAFT")) throw new MailError("mail_provider_unavailable");
    return result;
  };
  const replyHeaders = async (input: PreparedDraft, signal: AbortSignal, currentMessageId?: string) => {
    if (!input.threadId) return {};
    const query = new URLSearchParams({ format: "metadata" }); for (const name of ["Message-ID", "References", "Subject"]) query.append("metadataHeaders", name);
    const thread = parse(replySchema, await request(["users", "me", "threads", input.threadId], query, signal));
    if (thread.id !== input.threadId || thread.messages.some((row) => row.threadId && row.threadId !== input.threadId)) throw new MailError("mail_provider_unavailable");
    const parent = thread.messages.filter((row) => !row.labelIds?.includes("DRAFT") && row.id !== currentMessageId)
      .sort((a, b) => Number(BigInt(b.internalDate ?? "0") - BigInt(a.internalDate ?? "0")))[0];
    // A new standalone draft has its own thread; editing it isn't a reply.
    if (!parent && currentMessageId && thread.messages.some((row) => row.id === currentMessageId)) return {};
    if (!parent) throw new MailError("mail_invalid_request");
    const header = (name: string) => parent.payload?.headers.find((item) => item.name.toLowerCase() === name)?.value.trim() ?? "";
    const inReplyTo = header("message-id");
    if (!messageId.test(inReplyTo) || subjectKey(input.subject) !== subjectKey(header("subject"))) throw new MailError("mail_invalid_request");
    const references = (header("references").match(/<[^<>\s]+>/g) ?? []).filter((id) => messageId.test(id));
    return { inReplyTo, references: [...new Set([...references.slice(-49), inReplyTo])] };
  };
  return {
    createDraft: async (input: PreparedDraft, signal: AbortSignal) => {
      const raw = await renderGmailDraft(input, await replyHeaders(input, signal));
      const response = await guard(() => request(["users", "me", "drafts"], new URLSearchParams(), signal, { method: "POST", body: { message: { raw, ...(input.threadId ? { threadId: input.threadId } : {}) } } }));
      await created(parse(z.object({ id: idSchema }), response).id); return normalize(response);
    },
    updateDraft: async (id: string, input: PreparedDraft, signal: AbortSignal) => {
      const current = await existing(id, signal), updated = { ...input, threadId: input.threadId || current.message.threadId };
      const raw = await renderGmailDraft(updated, await replyHeaders(updated, signal, current.message.id));
      return normalize(await guard(() => request(["users", "me", "drafts", id], new URLSearchParams(), signal, { method: "PUT", body: { id, message: { raw, threadId: updated.threadId } } })), id);
    },
    sendDraft: async (id: string, signal: AbortSignal): Promise<MailMessage> => {
      await existing(id, signal);
      const raw = await guard(() => request(["users", "me", "drafts", "send"], new URLSearchParams(), signal, { method: "POST", body: { id } }));
      const source = parse(z.object({ id: idSchema, threadId: idSchema }).passthrough(), raw);
      return normalizeGmailMessage(lease.account.accountId, source.threadId, source);
    },
  };
}
