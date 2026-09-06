import emailAddresses from "email-addresses";
import { MailDraftInputSchema, type MailDraftInput } from "@misty/contracts";
import { MailError } from "../errors.js";

/** Only this private representation contains decoded attachment bytes. */
export function prepareDraft(raw: MailDraftInput) {
  const result = MailDraftInputSchema.safeParse(raw);
  if (!result.success) throw new MailError("mail_invalid_request");
  const input = result.data;
  const addresses = (values: typeof input.to = []) => values.map((value) => {
    const email = value.email.trim(), name = value.name?.trim() ?? "";
    const parsed = emailAddresses.parseOneAddress({ input: email, partial: false, rfc6532: true });
    if (!parsed || parsed.type !== "mailbox" || parsed.address.toLowerCase() !== email.toLowerCase() || /[\x00-\x08\x0b-\x1f\x7f-\x9f]/.test(name + email)) throw new MailError("mail_invalid_request");
    return { name, address: parsed.address };
  });
  const attachments = (input.attachments ?? []).map((attachment) => {
    const data = Buffer.from(attachment.data, "base64"), filename = attachment.filename.trim(), contentId = attachment.content_id?.trim() ?? "";
    // Buffer's decoder is permissive. The SDK schema checks shape; roundtrip
    // validation also rejects noncanonical padding bits before provider access.
    if (data.toString("base64") !== attachment.data || !filename || /[\x00-\x08\x0b-\x1f\x7f-\x9f]/.test(filename + contentId)) throw new MailError("mail_invalid_request");
    const mediaType = attachment.content_type.split(";", 1)[0]!.trim().toLowerCase();
    return { filename, contentType: /^[a-z0-9!#$&^_.+-]+\/[a-z0-9!#$&^_.+-]+$/.test(mediaType) ? mediaType : "application/octet-stream", data, inline: attachment.inline, contentId };
  });
  return { threadId: input.thread_id?.trim() ?? "", to: addresses(input.to), cc: addresses(input.cc), bcc: addresses(input.bcc), replyTo: addresses(input.reply_to),
    subject: input.subject.trim(), text: input.text, attachments };
}
export type PreparedDraft = ReturnType<typeof prepareDraft>;
