import { z } from "zod";
import { EmptyParamsSchema, IdentifierSchema, SpacePathSchema, } from "./requests.js";
/** Provider IDs are opaque, not Misty UUIDs. Reserved characters are encoded once by the server router. */
export const MailProviderIdSchema = z
    .string()
    .regex(/^[\x21-\x7e]{1,320}$/)
    .refine((value) => value !== "." && value !== "..", "Invalid provider ID");
const count = z.number().int().nonnegative();
const addresses = () => z.array(MailAddressSchema);
export const MailAddressSchema = z.object({
    name: z.string().optional(),
    email: z.string(),
});
export const MailAccountSchema = z.object({
    connection_id: z.string(),
    provider: z.string(),
    account_id: z.string(),
    email: z.string(),
    display_name: z.string(),
    total: count,
    unread: count,
    status: z.string().optional(),
    error_code: z.string().optional(),
});
export const MailFolderSchema = z.object({
    provider: z.string(),
    provider_id: z.string(),
    account_id: z.string(),
    name: z.string(),
    kind: z.string(),
    system: z.boolean(),
    total: count,
    unread: count,
    text_color: z.string().optional(),
    background: z.string().optional(),
});
export const MailAttachmentSchema = z.object({
    provider: z.string(),
    provider_id: z.string(),
    account_id: z.string(),
    message_id: z.string(),
    filename: z.string(),
    content_type: z.string(),
    size: count,
    inline: z.boolean(),
    content_id: z.string().optional(),
});
export const MailMessageSchema = z.object({
    provider: z.string(),
    provider_id: z.string(),
    account_id: z.string(),
    thread_id: z.string(),
    rfc822_id: z.string().optional(),
    subject: z.string(),
    from: MailAddressSchema,
    to: addresses(),
    cc: addresses(),
    bcc: addresses(),
    reply_to: addresses(),
    sent_at: z.string(),
    snippet: z.string(),
    body: z.object({
        text: z.string(),
        html: z.string().optional(),
        had_html: z.boolean(),
        truncated: z.boolean(),
    }),
    labels: z.array(z.string()),
    unread: z.boolean(),
    starred: z.boolean(),
    draft: z.boolean(),
    attachments: z.array(MailAttachmentSchema),
});
export const MailThreadSchema = z.object({
    provider: z.string(),
    provider_id: z.string(),
    account_id: z.string(),
    subject: z.string(),
    snippet: z.string(),
    participants: addresses(),
    labels: z.array(z.string()),
    last_message_at: z.string(),
    unread: z.boolean(),
    starred: z.boolean(),
    messages: z.array(MailMessageSchema),
});
export const MailDraftSchema = z.object({
    provider: z.string(),
    provider_id: z.string(),
    account_id: z.string(),
    thread_id: z.string().optional(),
    message: MailMessageSchema,
});
const header = (max) => z
    .string()
    .max(max)
    .refine((value) => !/[\r\n\u0000]/.test(value), "Invalid mail header");
const addressInput = z.strictObject({
    name: header(1024).optional(),
    email: header(320).min(1),
});
const attachment = z.strictObject({
    filename: header(1024).min(1),
    content_type: header(256).min(1),
    data: z
        .string()
        .max(14 * 1024 * 1024)
        .refine((value) => value.length % 4 === 0 && /^[A-Za-z0-9+/]*={0,2}$/.test(value), "Invalid base64 attachment"),
    inline: z.boolean(),
    content_id: header(1024).optional(),
});
/** Matches the existing providers' 10 MiB combined body/attachment bound. */
export const MISTY_MAIL_CONTENT_MAX_BYTES = 10 * 1024 * 1024;
export const MISTY_MAIL_JSON_MAX_BYTES = 28 * 1024 * 1024;
export const MailDraftInputSchema = z
    .strictObject({
    connection_id: IdentifierSchema,
    thread_id: MailProviderIdSchema.optional(),
    to: z.array(addressInput).max(500),
    cc: z.array(addressInput).max(500).optional(),
    bcc: z.array(addressInput).max(500).optional(),
    reply_to: z.array(addressInput).max(500).optional(),
    subject: header(8192),
    text: z.string().max(MISTY_MAIL_CONTENT_MAX_BYTES),
    attachments: z.array(attachment).max(100).optional(),
})
    .refine((value) => {
    const bytes = new TextEncoder().encode(value.text).length +
        (value.attachments ?? []).reduce((total, file) => total +
            (file.data.length / 4) * 3 -
            (file.data.endsWith("==") ? 2 : file.data.endsWith("=") ? 1 : 0), 0);
    return bytes <= MISTY_MAIL_CONTENT_MAX_BYTES;
}, "Email body and attachments are limited to 10 MiB combined.")
    .refine((value) => new TextEncoder().encode(JSON.stringify(value)).length <=
    MISTY_MAIL_JSON_MAX_BYTES, "Encoded email draft exceeds 28 MiB.");
export const MailThreadActionSchema = z
    .strictObject({
    connection_id: IdentifierSchema,
    read: z.boolean().optional(),
    archived: z.boolean().optional(),
    starred: z.boolean().optional(),
})
    .refine((value) => value.read !== undefined ||
    value.archived !== undefined ||
    value.starred !== undefined, "Choose a supported mail action.");
const path = { path: SpacePathSchema.optional() };
const connection = z.strictObject({ connection_id: IdentifierSchema });
const draftPath = SpacePathSchema.extend({ draftID: MailProviderIdSchema });
const threadPath = SpacePathSchema.extend({ threadID: MailProviderIdSchema });
export const mistyMailContracts = {
    "mail.accounts.list": {
        verb: "GET",
        path: "/mail/accounts",
        params: EmptyParamsSchema,
        result: z.object({ accounts: z.array(MailAccountSchema) }),
    },
    "mail.folders.list": {
        verb: "GET",
        path: "/mail/folders",
        params: z.strictObject({ ...path, query: connection }),
        result: z.object({ folders: z.array(MailFolderSchema) }),
    },
    "mail.threads.list": {
        verb: "GET",
        path: "/mail/threads",
        params: z.strictObject({
            ...path,
            query: connection.extend({
                folder_id: z.string().max(320).optional(),
                query: z.string().max(2000).optional(),
                page_token: z.string().max(4096).optional(),
                page_size: z.number().int().min(1).max(100).optional(),
            }),
        }),
        result: z.object({
            threads: z.array(MailThreadSchema),
            next_page_token: z.string().optional(),
            estimated_total: count.optional(),
        }),
    },
    "mail.threads.get": {
        verb: "GET",
        path: "/mail/threads/{threadID}",
        params: z.strictObject({ path: threadPath, query: connection }),
        result: z.object({ thread: MailThreadSchema }),
    },
    "mail.threads.action": {
        verb: "POST",
        path: "/mail/threads/{threadID}/actions",
        params: z.strictObject({ path: threadPath, body: MailThreadActionSchema }),
        result: z.object({
            thread_id: z.string(),
            added_labels: z.array(z.string()),
            removed_labels: z.array(z.string()),
        }),
    },
    "mail.drafts.create": {
        verb: "POST",
        path: "/mail/drafts",
        params: z.strictObject({ ...path, body: MailDraftInputSchema }),
        result: z.object({ draft: MailDraftSchema }),
    },
    "mail.drafts.update": {
        verb: "PUT",
        path: "/mail/drafts/{draftID}",
        params: z.strictObject({ path: draftPath, body: MailDraftInputSchema }),
        result: z.object({ draft: MailDraftSchema }),
    },
    "mail.drafts.send": {
        verb: "POST",
        path: "/mail/drafts/{draftID}/send",
        params: z.strictObject({
            path: draftPath,
            body: connection.extend({
                authoring_source: z.enum(["user", "ai"]),
                confirmed: z.literal(true),
            }),
        }),
        result: z.object({ message: MailMessageSchema }),
    },
};
//# sourceMappingURL=mail.js.map