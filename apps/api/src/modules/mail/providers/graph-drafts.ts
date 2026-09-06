import { z } from "zod";
import type { MailDraft, MailMessage } from "@misty/contracts";
import type { ConnectionTokenLease } from "../../connections/token-broker.js";
import { MailError } from "../errors.js";
import type { PreparedDraft } from "./draft-input.js";
import type { MailWriteGuard } from "./actions.js";
import { createMailTransport } from "./transport.js";
import { graphMessageSchema, normalizeGraphThreads } from "./graph-thread.js";
import { graphNextToken } from "./pagination.js";
import { compareTime, graphTime } from "./format.js";
import { uploadGraphAttachment } from "./graph-upload.js";

const idSchema = z.string().regex(/^[\x21-\x7e]{1,320}$/).refine((id) => id !== "." && id !== "..");
const messageSelect = "id,conversationId,internetMessageId,subject,bodyPreview,body,from,toRecipients,ccRecipients,bccRecipients,replyTo,receivedDateTime,sentDateTime,isRead,isDraft,parentFolderId,flag,hasAttachments";
const pageSchema = z.object({ value: z.array(z.unknown()).nullish().transform((rows) => rows ?? []), "@odata.nextLink": z.string().nullish().transform((value) => value ?? "") });
function parse<T>(schema: z.ZodType<T>, raw: unknown): T { const result = schema.safeParse(raw); if (!result.success) throw new MailError("mail_provider_unavailable"); return result.data; }

export function createGraphDraftWriter(lease: ConnectionTokenLease, guard: MailWriteGuard, fetcher: typeof fetch = fetch, created: (id: string) => Promise<void> = async () => {}) {
  const request = createMailTransport(lease, fetcher);
  const write = (parts: string[], method: "POST" | "PATCH" | "DELETE", body: unknown, signal: AbortSignal, discardResponse = false) =>
    guard(() => request(parts, new URLSearchParams(), signal, { method, body, discardResponse }));
  const getMessage = async (id: string, signal: AbortSignal) => {
    if (!idSchema.safeParse(id).success) throw new MailError("mail_invalid_request");
    const source = parse(graphMessageSchema, await request(["me", "messages", id], new URLSearchParams({ $select: messageSelect, $expand: "attachments($select=id,name,contentType,size,isInline,contentId)" }), signal));
    if (source.id !== id) throw new MailError("mail_provider_unavailable");
    if (!source.isDraft) throw new MailError("mail_invalid_request"); return source;
  };
  const normalize = (raw: unknown, expectedId?: string): MailDraft => {
    const source = parse(graphMessageSchema, raw);
    if (!idSchema.safeParse(source.id).success || expectedId && source.id !== expectedId || !source.isDraft) throw new MailError("mail_provider_unavailable");
    const message = normalizeGraphThreads(lease.account.accountId, [{ ...source, conversationId: source.conversationId || source.id }])[0]!.messages[0]!;
    return { provider: "outlook", provider_id: source.id, account_id: lease.account.accountId, thread_id: message.thread_id, message };
  };
  const pages = async (parts: string[], query: URLSearchParams, signal: AbortSignal) => {
    const rows: unknown[] = [], tokens = new Set<string>();
    for (let index = 0; index < 20; index++) {
      const page = parse(pageSchema, await request(parts, query, signal));
      rows.push(...page.value); if (rows.length > 500) throw new MailError("mail_response_too_large");
      const token = graphNextToken(page["@odata.nextLink"]); if (!token) return rows;
      if (tokens.has(token)) throw new MailError("mail_response_too_large"); tokens.add(token); query.set("$skiptoken", token);
    }
    throw new MailError("mail_response_too_large");
  };
  const parent = async (input: PreparedDraft, signal: AbortSignal) => {
    const rows = await pages(["me", "messages"], new URLSearchParams({ $select: "id,conversationId,subject,isDraft,sentDateTime,receivedDateTime", $top: "100", $filter: `conversationId eq '${input.threadId.replaceAll("'", "''")}'` }), signal);
    const messages = rows.map((row) => parse(graphMessageSchema, row));
    if (messages.some((row) => !idSchema.safeParse(row.id).success || row.conversationId !== input.threadId)) throw new MailError("mail_provider_unavailable");
    const message = messages.filter((row) => !row.isDraft).sort((a, b) => compareTime(graphTime(b.sentDateTime, b.receivedDateTime), graphTime(a.sentDateTime, a.receivedDateTime)))[0];
    if (!message) throw new MailError("mail_provider_item_not_found");
    if (message.subject.trim().replace(/^(?:re:\s*)+/i, "") !== input.subject.trim().replace(/^(?:re:\s*)+/i, "")) throw new MailError("mail_invalid_request");
    return message.id;
  };
  const payload = (input: PreparedDraft) => {
    const recipients = (addresses: PreparedDraft["to"]) => addresses.map((emailAddress) => ({ emailAddress }));
    // Empty arrays intentionally clear recipients when editing an existing draft.
    return { subject: input.subject, body: { contentType: "Text", content: input.text }, toRecipients: recipients(input.to), ccRecipients: recipients(input.cc),
      bccRecipients: recipients(input.bcc), replyTo: recipients(input.replyTo) };
  };
  const addAttachments = async (id: string, input: PreparedDraft, signal: AbortSignal) => {
    for (const file of input.attachments) {
      if (file.data.length < 3_000_000) {
        await write(["me", "messages", id, "attachments"], "POST", { "@odata.type": "#microsoft.graph.fileAttachment", name: file.filename, contentType: file.contentType,
          contentBytes: file.data.toString("base64"), isInline: file.inline, ...(file.contentId ? { contentId: file.contentId } : {}) }, signal, true);
      } else {
        const session = await write(["me", "messages", id, "attachments", "createUploadSession"], "POST", { AttachmentItem: { attachmentType: "file", name: file.filename,
          size: file.data.length, contentType: file.contentType, isInline: file.inline, ...(file.contentId ? { contentId: file.contentId } : {}) } }, signal);
        await uploadGraphAttachment(session, file.data, signal, guard, fetcher);
      }
    }
  };
  return {
    createDraft: async (input: PreparedDraft, signal: AbortSignal) => {
      const source = input.threadId
        ? await write(["me", "messages", await parent(input, signal), "createReply"], "POST", {}, signal)
        : await write(["me", "messages"], "POST", payload(input), signal);
      await created(parse(z.object({ id: idSchema }), source).id); const draft = normalize(source);
      if (input.threadId && draft.thread_id !== input.threadId) throw new MailError("mail_provider_unavailable");
      if (input.threadId) await write(["me", "messages", draft.provider_id], "PATCH", payload(input), signal, true);
      await addAttachments(draft.provider_id, input, signal);
      if (input.threadId || input.attachments.length) return normalize(await getMessage(draft.provider_id, signal), draft.provider_id);
      return draft;
    },
    updateDraft: async (id: string, input: PreparedDraft, signal: AbortSignal) => {
      const current = await getMessage(id, signal);
      if (input.threadId && input.threadId !== (current.conversationId || current.id)) throw new MailError("mail_invalid_request");
      const existing = await pages(["me", "messages", id, "attachments"], new URLSearchParams({ $select: "id", $top: "100" }), signal);
      const attachmentIds = [...new Set(existing.map((raw) => parse(z.object({ id: idSchema }), raw).id))];
      await write(["me", "messages", id], "PATCH", payload(input), signal, true);
      for (const attachment of attachmentIds) await write(["me", "messages", id, "attachments", attachment], "DELETE", undefined, signal, true);
      await addAttachments(id, input, signal);
      return normalize(await getMessage(id, signal), id);
    },
    sendDraft: async (id: string, signal: AbortSignal): Promise<MailMessage> => {
      const source = await getMessage(id, signal), draft = normalize(source, id);
      // Validate and normalize before the irreversible send. This endpoint takes
      // no message body and cannot manufacture an unreviewed draft.
      await write(["me", "messages", id, "send"], "POST", undefined, signal, true);
      return { ...draft.message, draft: false, labels: draft.message.labels.filter((label) => label !== "DRAFT") };
    },
  };
}
