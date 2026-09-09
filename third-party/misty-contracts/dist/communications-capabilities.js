import { z } from "zod";
import { MistyCapabilityDefinitionSchema, MistyCapabilityEvidenceSchema, } from "./capabilities.js";
const reference = z.string().min(1).max(2048);
const recipient = z.strictObject({
    address: z.string().min(1).max(320),
    displayName: z.string().max(200).optional(),
});
const attachment = z.strictObject({
    reference,
    name: z.string().min(1).max(255),
    mimeType: z.string().max(200).optional(),
    size: z.number().int().nonnegative().optional(),
});
export const MistyObservedMessageSchema = z.strictObject({
    reference,
    threadReference: reference,
    subject: z.string().max(2000).optional(),
    sender: recipient.optional(),
    recipients: z.array(recipient).max(100),
    text: z.string().max(64000),
    observedAt: z.iso.datetime({ offset: true }),
    sentAt: z.iso.datetime({ offset: true }).optional(),
    attachments: z.array(attachment).max(100),
    truncated: z.boolean(),
});
export const MistyCommunicationReadResultSchema = z.strictObject({
    accountIdentity: z.string().min(1).max(320),
    sourceTargetId: z.uuid(),
    messages: z.array(MistyObservedMessageSchema).max(100),
    coverage: z.enum(["visible_page", "opened_thread", "visited_search_pages"]),
    partial: z.boolean(),
    truncated: z.boolean(),
    // A cursor is scoped to this provider, target and search. It is never a URL
    // that grants authority to navigate another account or origin.
    nextCursor: z.string().min(1).max(2048).optional(),
    limitations: z.array(z.string().min(1).max(500)).max(20),
    evidence: z.array(MistyCapabilityEvidenceSchema).min(1).max(100),
});
const readInput = z.strictObject({
    threadReference: reference.optional(),
    limit: z.number().int().min(1).max(100).default(20),
});
const searchInput = z.strictObject({
    query: z.string().min(1).max(2000),
    cursor: z.string().max(2048).optional(),
    limit: z.number().int().min(1).max(100).default(20),
});
const draftInput = z.strictObject({
    recipients: z.array(recipient).min(1).max(100),
    subject: z.string().max(2000).optional(),
    text: z.string().min(1).max(64000),
    replyTo: reference.optional(),
    attachments: z.array(attachment).max(20).default([]),
});
export const MistyCommunicationDraftResultSchema = z.strictObject({
    accountIdentity: z.string().min(1).max(320),
    threadReference: reference.optional(),
    sourceTargetId: z.uuid(),
    draftReference: reference,
    contentHash: z.string().regex(/^[a-f0-9]{64}$/),
    recipients: z.array(recipient).min(1).max(100),
    evidence: z.array(MistyCapabilityEvidenceSchema).min(1).max(100),
});
export const MistyCommunicationSendInputSchema = z.strictObject({
    draftReference: reference,
    expectedContentHash: z.string().regex(/^[a-f0-9]{64}$/),
    recipients: z.array(recipient).min(1).max(100),
});
export const MistyCommunicationSendResultSchema = z.strictObject({
    accountIdentity: z.string().min(1).max(320),
    threadReference: reference,
    sourceTargetId: z.uuid(),
    messageReference: reference,
    // Observing acceptance or a sent-folder entry is not proof of recipient delivery.
    confirmation: z.enum(["provider_accepted", "observed_sent_item"]),
    recipients: z.array(recipient).min(1).max(100),
    evidence: z.array(MistyCapabilityEvidenceSchema).min(1).max(100),
});
function definition(name, description, input, output, kind) {
    return MistyCapabilityDefinitionSchema.parse({
        name,
        version: 1,
        description,
        inputSchema: z.toJSONSchema(input),
        outputSchema: z.toJSONSchema(output),
        requiredScopes: [name],
        effects: {
            kind,
            approval: "scoped",
            retry: kind === "read" ? "idempotent" : "reconcile",
            incidental: kind === "read"
                ? [
                    "Opening a message or thread may mark it read. Results cover only the pages actually inspected.",
                ]
                : [],
        },
    });
}
/** Semantic contracts, shared unchanged by Gmail, Outlook and other validated providers. */
export const mistyInboxCapabilities = [
    definition("inbox.read", "Read visible messages or an identified thread on the bound mailbox. Report observed coverage and truncation.", readInput, MistyCommunicationReadResultSchema, "read"),
    definition("inbox.search", "Search the bound mailbox and return only observed results, with a continuation cursor when more pages remain.", searchInput, MistyCommunicationReadResultSchema, "read"),
    definition("inbox.draft", "Prepare and verify an unsent draft on the bound mailbox. Return its content hash and actual recipients.", draftInput, MistyCommunicationDraftResultSchema, "write"),
    definition("inbox.send", "Send the reviewed draft only if its content hash, recipients and account still match. Reconcile uncertainty; never infer delivery from a click.", MistyCommunicationSendInputSchema, MistyCommunicationSendResultSchema, "send"),
    definition("inbox.manage", "Apply one supported mailbox operation to explicitly identified messages and verify the resulting state.", z.strictObject({
        messages: z.array(reference).min(1).max(100),
        action: z.enum([
            "archive",
            "mark_read",
            "mark_unread",
            "move_to_trash",
            "label",
        ]),
        label: z.string().min(1).max(200).optional(),
    }), z.strictObject({
        sourceTargetId: z.uuid(),
        changed: z.array(reference).max(100),
        partial: z.boolean(),
        evidence: z.array(MistyCapabilityEvidenceSchema).min(1).max(100),
    }), "write"),
];
export const mistySocialCapabilities = [
    definition("social.read_thread", "Read the identified conversation on the bound social account. Report only observed content.", readInput, MistyCommunicationReadResultSchema, "read"),
    definition("social.search", "Search conversations on the bound social account, reporting inspected pages and partial coverage.", searchInput, MistyCommunicationReadResultSchema, "read"),
    definition("social.draft_message", "Prepare an unsent message for identified recipients in the bound social account.", draftInput, MistyCommunicationDraftResultSchema, "write"),
    definition("social.send_message", "Send the reviewed message only when the account, recipients and content hash still match. Return observed acceptance evidence.", MistyCommunicationSendInputSchema, MistyCommunicationSendResultSchema, "send"),
];
//# sourceMappingURL=communications-capabilities.js.map