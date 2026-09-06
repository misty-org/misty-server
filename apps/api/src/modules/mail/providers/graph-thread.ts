import { z } from "zod";
import type { MailAddress, MailMessage } from "@misty/contracts";
import { MailError } from "../errors.js";
import { cleanHeader, cleanText, compareTime, graphTime, groupThread, htmlToText, sortLabels } from "./format.js";

const string = z.string().nullish().transform((value) => value ?? ""), boolean = z.boolean().nullish().transform((value) => value ?? false);
const address = z.preprocess((value) => value ?? {}, z.object({ emailAddress: z.preprocess((value) => value ?? {}, z.object({ name: string, address: string })) }));
const addressList = z.array(address).nullish().transform((value) => value ?? []);
export const graphMessageSchema = z.object({ id: string, conversationId: string, internetMessageId: string, subject: string, bodyPreview: string,
  body: z.preprocess((value) => value ?? {}, z.object({ contentType: string, content: string })), from: address,
  toRecipients: addressList, ccRecipients: addressList, bccRecipients: addressList, replyTo: addressList,
  sentDateTime: string, receivedDateTime: string, isRead: boolean, isDraft: boolean, parentFolderId: string,
  flag: z.preprocess((value) => value ?? {}, z.object({ flagStatus: string })),
  attachments: z.array(z.object({ id: string, name: string, contentType: string, contentId: string, isInline: boolean,
    size: z.number().int().nonnegative().nullish().transform((value) => value ?? 0) })).nullish().transform((value) => value ?? []),
});
type GraphMessage = z.infer<typeof graphMessageSchema>;
const convertAddress = (value: z.infer<typeof address>): MailAddress => ({ name: cleanHeader(value.emailAddress.name), email: cleanHeader(value.emailAddress.address) });
const convertAddresses = (values: z.infer<typeof addressList>) => values.map(convertAddress).filter((value) => !!value.email);
export function normalizeGraphThreads(accountId: string, values: unknown[]) {
  const groups = new Map<string, GraphMessage[]>();
  for (const raw of values) {
    const result = graphMessageSchema.safeParse(raw);
    if (!result.success || !result.data.id.trim() || !result.data.conversationId.trim()) throw new MailError("mail_provider_unavailable");
    const row = result.data, group = groups.get(row.conversationId) ?? []; group.push(row); groups.set(row.conversationId, group);
  }
  return [...groups].map(([id, messages]) => {
    let bytes = 0;
    const normalized: MailMessage[] = messages.map((source) => {
      bytes += Buffer.byteLength(source.body.content); if (bytes > 10 * 1024 * 1024) throw new MailError("mail_body_too_large");
      const kind = source.body.contentType.trim().toLowerCase();
      if (!["", "text", "html"].includes(kind)) throw new MailError("mail_provider_unavailable");
      const labels = sortLabels([...(source.parentFolderId ? [source.parentFolderId] : []), ...(!source.isRead ? ["UNREAD"] : []),
        ...(source.flag.flagStatus.toLowerCase() === "flagged" ? ["STARRED"] : []), ...(source.isDraft ? ["DRAFT"] : [])]);
      const html = kind === "html" ? cleanText(source.body.content) : "";
      return { provider: "outlook", provider_id: source.id, account_id: accountId, thread_id: source.conversationId, rfc822_id: cleanHeader(source.internetMessageId),
        subject: cleanHeader(source.subject), from: convertAddress(source.from), to: convertAddresses(source.toRecipients), cc: convertAddresses(source.ccRecipients),
        bcc: convertAddresses(source.bccRecipients), reply_to: convertAddresses(source.replyTo), sent_at: graphTime(source.sentDateTime, source.receivedDateTime),
        snippet: cleanText(source.bodyPreview), body: { text: kind === "html" ? cleanText(htmlToText(source.body.content)) : cleanText(source.body.content),
          ...(html ? { html } : {}), had_html: kind === "html", truncated: false },
        labels, unread: !source.isRead, starred: source.flag.flagStatus.toLowerCase() === "flagged", draft: source.isDraft,
        attachments: source.attachments.map((attachment, index) => ({ provider: "outlook", provider_id: attachment.id || `${source.id}:attachment:${index}`, account_id: accountId,
          message_id: source.id, filename: cleanHeader(attachment.name), content_type: cleanHeader(attachment.contentType), size: attachment.size, inline: attachment.isInline, content_id: cleanHeader(attachment.contentId) })) };
    });
    return groupThread("outlook", accountId, id, normalized);
  }).sort((a, b) => compareTime(b.last_message_at, a.last_message_at));
}
