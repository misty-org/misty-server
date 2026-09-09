import { z } from "zod";
export declare const MistyObservedMessageSchema: z.ZodObject<{
    reference: z.ZodString;
    threadReference: z.ZodString;
    subject: z.ZodOptional<z.ZodString>;
    sender: z.ZodOptional<z.ZodObject<{
        address: z.ZodString;
        displayName: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
    recipients: z.ZodArray<z.ZodObject<{
        address: z.ZodString;
        displayName: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
    text: z.ZodString;
    observedAt: z.ZodISODateTime;
    sentAt: z.ZodOptional<z.ZodISODateTime>;
    attachments: z.ZodArray<z.ZodObject<{
        reference: z.ZodString;
        name: z.ZodString;
        mimeType: z.ZodOptional<z.ZodString>;
        size: z.ZodOptional<z.ZodNumber>;
    }, z.core.$strict>>;
    truncated: z.ZodBoolean;
}, z.core.$strict>;
export declare const MistyCommunicationReadResultSchema: z.ZodObject<{
    accountIdentity: z.ZodString;
    sourceTargetId: z.ZodUUID;
    messages: z.ZodArray<z.ZodObject<{
        reference: z.ZodString;
        threadReference: z.ZodString;
        subject: z.ZodOptional<z.ZodString>;
        sender: z.ZodOptional<z.ZodObject<{
            address: z.ZodString;
            displayName: z.ZodOptional<z.ZodString>;
        }, z.core.$strict>>;
        recipients: z.ZodArray<z.ZodObject<{
            address: z.ZodString;
            displayName: z.ZodOptional<z.ZodString>;
        }, z.core.$strict>>;
        text: z.ZodString;
        observedAt: z.ZodISODateTime;
        sentAt: z.ZodOptional<z.ZodISODateTime>;
        attachments: z.ZodArray<z.ZodObject<{
            reference: z.ZodString;
            name: z.ZodString;
            mimeType: z.ZodOptional<z.ZodString>;
            size: z.ZodOptional<z.ZodNumber>;
        }, z.core.$strict>>;
        truncated: z.ZodBoolean;
    }, z.core.$strict>>;
    coverage: z.ZodEnum<{
        visible_page: "visible_page";
        opened_thread: "opened_thread";
        visited_search_pages: "visited_search_pages";
    }>;
    partial: z.ZodBoolean;
    truncated: z.ZodBoolean;
    nextCursor: z.ZodOptional<z.ZodString>;
    limitations: z.ZodArray<z.ZodString>;
    evidence: z.ZodArray<z.ZodObject<{
        targetId: z.ZodString;
        observedAt: z.ZodISODateTime;
        kind: z.ZodEnum<{
            browser: "browser";
            provider: "provider";
            resource: "resource";
            command: "command";
        }>;
        reference: z.ZodString;
        revision: z.ZodOptional<z.ZodString>;
        excerpt: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
}, z.core.$strict>;
export declare const MistyCommunicationDraftResultSchema: z.ZodObject<{
    accountIdentity: z.ZodString;
    threadReference: z.ZodOptional<z.ZodString>;
    sourceTargetId: z.ZodUUID;
    draftReference: z.ZodString;
    contentHash: z.ZodString;
    recipients: z.ZodArray<z.ZodObject<{
        address: z.ZodString;
        displayName: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
    evidence: z.ZodArray<z.ZodObject<{
        targetId: z.ZodString;
        observedAt: z.ZodISODateTime;
        kind: z.ZodEnum<{
            browser: "browser";
            provider: "provider";
            resource: "resource";
            command: "command";
        }>;
        reference: z.ZodString;
        revision: z.ZodOptional<z.ZodString>;
        excerpt: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
}, z.core.$strict>;
export declare const MistyCommunicationSendInputSchema: z.ZodObject<{
    draftReference: z.ZodString;
    expectedContentHash: z.ZodString;
    recipients: z.ZodArray<z.ZodObject<{
        address: z.ZodString;
        displayName: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
}, z.core.$strict>;
export declare const MistyCommunicationSendResultSchema: z.ZodObject<{
    accountIdentity: z.ZodString;
    threadReference: z.ZodString;
    sourceTargetId: z.ZodUUID;
    messageReference: z.ZodString;
    confirmation: z.ZodEnum<{
        provider_accepted: "provider_accepted";
        observed_sent_item: "observed_sent_item";
    }>;
    recipients: z.ZodArray<z.ZodObject<{
        address: z.ZodString;
        displayName: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
    evidence: z.ZodArray<z.ZodObject<{
        targetId: z.ZodString;
        observedAt: z.ZodISODateTime;
        kind: z.ZodEnum<{
            browser: "browser";
            provider: "provider";
            resource: "resource";
            command: "command";
        }>;
        reference: z.ZodString;
        revision: z.ZodOptional<z.ZodString>;
        excerpt: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
}, z.core.$strict>;
/** Semantic contracts, shared unchanged by Gmail, Outlook and other validated providers. */
export declare const mistyInboxCapabilities: {
    name: string;
    version: number;
    description: string;
    inputSchema: Record<string, z.core.util.JSONType>;
    outputSchema: Record<string, z.core.util.JSONType>;
    requiredScopes: string[];
    effects: {
        kind: "read" | "write" | "send" | "execute" | "destructive";
        incidental: string[];
        approval: "interactive" | "none" | "scoped";
        retry: "never" | "read_only" | "idempotent" | "reconcile";
    };
}[];
export declare const mistySocialCapabilities: {
    name: string;
    version: number;
    description: string;
    inputSchema: Record<string, z.core.util.JSONType>;
    outputSchema: Record<string, z.core.util.JSONType>;
    requiredScopes: string[];
    effects: {
        kind: "read" | "write" | "send" | "execute" | "destructive";
        incidental: string[];
        approval: "interactive" | "none" | "scoped";
        retry: "never" | "read_only" | "idempotent" | "reconcile";
    };
}[];
