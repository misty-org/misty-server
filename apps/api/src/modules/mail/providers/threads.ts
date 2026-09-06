import { z } from "zod";
import type { MailThread } from "@misty/contracts";
import type { ConnectionTokenLease } from "../../connections/token-broker.js";
import { MailError } from "../errors.js";
import { cleanText, groupThread } from "./format.js";
import { gmailThreadSchema, normalizeGmailThread } from "./gmail-thread.js";
import { normalizeGraphThreads } from "./graph-thread.js";
import { graphNextToken } from "./pagination.js";
import type { createMailTransport } from "./transport.js";

export type ThreadQuery = { pageSize: number; pageToken: string; query: string; folderId: string };
const graphSelect = "id,conversationId,internetMessageId,subject,bodyPreview,from,toRecipients,ccRecipients,bccRecipients,replyTo,receivedDateTime,sentDateTime,isRead,isDraft,parentFolderId,flag";
const pageSchema = z.object({ value: z.array(z.unknown()).nullish().transform((value) => value ?? []), "@odata.nextLink": z.string().nullish().transform((value) => value ?? "") });
function parse<T>(schema: z.ZodType<T>, input: unknown): T { const result = schema.safeParse(input); if (!result.success) throw new MailError("mail_provider_unavailable"); return result.data; }
const quoted = (value: string) => `'${value.replaceAll("'", "''")}'`;

export function createThreadReader(lease: ConnectionTokenLease, request: ReturnType<typeof createMailTransport>) {
  const google = lease.account.provider === "google", accountId = lease.account.accountId;
  return {
    threads: async (input: ThreadQuery, signal: AbortSignal) => {
      if (google) {
        const query = new URLSearchParams({ maxResults: String(input.pageSize) });
        if (input.pageToken) query.set("pageToken", input.pageToken); if (input.query) query.set("q", input.query); if (input.folderId) query.set("labelIds", input.folderId);
        const page = parse(z.object({ threads: z.array(gmailThreadSchema).nullish().transform((value) => value ?? []), nextPageToken: z.string().nullish().transform((value) => value ?? ""),
          resultSizeEstimate: z.number().int().nonnegative().nullish().transform((value) => value ?? 0) }), await request(["users", "me", "threads"], query, signal));
        if (page.threads.length > 100) throw new MailError("mail_response_too_large");
        const raw = page.threads.filter((thread) => !!thread.id.trim()), threads: MailThread[] = Array(raw.length); let next = 0;
        if (raw.some((thread) => !/^[\x21-\x7e]{1,320}$/.test(thread.id) || thread.id === "." || thread.id === "..")) throw new MailError("mail_provider_unavailable");
        const controller = new AbortController(), workSignal = AbortSignal.any([signal, controller.signal]);
        await Promise.all(Array.from({ length: Math.min(10, raw.length) }, async () => {
          while (next < raw.length && !workSignal.aborted) {
            const index = next++, thread = raw[index]!;
            if (thread.messages.length) { threads[index] = normalizeGmailThread(accountId, thread); continue; }
            try {
              const metadata = new URLSearchParams({ format: "metadata" }); for (const name of ["Subject", "From", "To", "Cc", "Date"]) metadata.append("metadataHeaders", name);
              const detail = normalizeGmailThread(accountId, await request(["users", "me", "threads", thread.id], metadata, workSignal));
              if (detail.provider_id !== thread.id) throw new MailError("mail_provider_unavailable");
              threads[index] = detail;
            } catch (error) {
              if (error instanceof MailError && ["mail_provider_authorization_failed", "mail_provider_rate_limited", "mail_response_too_large", "mail_body_too_large"].includes(error.code)) throw error;
              threads[index] = groupThread("gmail", accountId, thread.id, [], cleanText(thread.snippet));
            }
          }
        })).finally(() => controller.abort());
        if (signal.aborted) throw new MailError("mail_provider_unavailable");
        return { threads, next_page_token: page.nextPageToken, estimated_total: page.resultSizeEstimate };
      }
      const query = new URLSearchParams({ $select: graphSelect, $orderby: "receivedDateTime desc", $top: String(input.pageSize) });
      if (input.pageToken) query.set("$skiptoken", input.pageToken);
      if (input.query) { query.set("$search", `"${input.query.replaceAll('"', '\\"')}"`); query.delete("$orderby"); }
      if (input.folderId) { query.set("$filter", `(parentFolderId eq ${quoted(input.folderId)})`); query.delete("$orderby"); }
      const page = parse(pageSchema, await request(["me", "messages"], query, signal));
      if (page.value.length > 500) throw new MailError("mail_response_too_large");
      const threads = normalizeGraphThreads(accountId, page.value);
      return { threads, next_page_token: graphNextToken(page["@odata.nextLink"]), estimated_total: threads.length };
    },
    thread: async (id: string, signal: AbortSignal) => {
      if (google) {
        const thread = normalizeGmailThread(accountId, await request(["users", "me", "threads", id], new URLSearchParams({ format: "full" }), signal));
        if (thread.provider_id !== id) throw new MailError("mail_provider_unavailable"); return thread;
      }
      const query = new URLSearchParams({ $select: `${graphSelect},body,hasAttachments`, $top: "100", $filter: `conversationId eq ${quoted(id)}`, $expand: "attachments($select=id,name,contentType,size,isInline)" });
      const messages: unknown[] = [], seen = new Set<string>(); let retried = false;
      for (let index = 0; index < 20; index++) {
        let raw: unknown;
        try { raw = await request(["me", "messages"], query, signal); }
        catch (error) {
          if (!retried && index === 0 && error instanceof MailError && error.providerStatus === 400) {
            retried = true; query.delete("$expand"); raw = await request(["me", "messages"], query, signal);
          } else throw error;
        }
        const page = parse(pageSchema, raw); messages.push(...page.value); if (messages.length > 500) throw new MailError("mail_response_too_large");
        const token = graphNextToken(page["@odata.nextLink"]);
        if (!token) {
          const thread = normalizeGraphThreads(accountId, messages).find((thread) => thread.provider_id === id);
          if (!thread) throw new MailError("mail_provider_item_not_found"); return thread;
        }
        if (seen.has(token)) throw new MailError("mail_response_too_large"); seen.add(token); query.set("$skiptoken", token);
      }
      throw new MailError("mail_response_too_large");
    },
  };
}
