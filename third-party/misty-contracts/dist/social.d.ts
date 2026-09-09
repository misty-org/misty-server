import { z } from 'zod';
export declare const socialOperations: readonly ["messages", "sendMessage", "runDetail", "decideRun", "cancelRun", "retryRun", "updateMessage", "deleteMessage", "addMessageReaction", "removeMessageReaction", "markRead", "conversations", "createConversation", "updateConversation", "deleteDisconnectedConversation", "deleteOrClearConversation", "clearEveryoneConversation", "markConversationRead", "conversationMessages", "sendConversationMessage", "updateConversationMessage", "deleteConversationMessage", "addConversationMessageReaction", "removeConversationMessageReaction", "members", "roadmaps", "nodes"];
export declare const mistySocialContracts: {
    readonly 'social.perform': {
        readonly params: z.ZodObject<{
            operation: z.ZodEnum<{
                roadmaps: "roadmaps";
                messages: "messages";
                nodes: "nodes";
                members: "members";
                sendMessage: "sendMessage";
                runDetail: "runDetail";
                decideRun: "decideRun";
                cancelRun: "cancelRun";
                retryRun: "retryRun";
                updateMessage: "updateMessage";
                deleteMessage: "deleteMessage";
                addMessageReaction: "addMessageReaction";
                removeMessageReaction: "removeMessageReaction";
                markRead: "markRead";
                conversations: "conversations";
                createConversation: "createConversation";
                updateConversation: "updateConversation";
                deleteDisconnectedConversation: "deleteDisconnectedConversation";
                deleteOrClearConversation: "deleteOrClearConversation";
                clearEveryoneConversation: "clearEveryoneConversation";
                markConversationRead: "markConversationRead";
                conversationMessages: "conversationMessages";
                sendConversationMessage: "sendConversationMessage";
                updateConversationMessage: "updateConversationMessage";
                deleteConversationMessage: "deleteConversationMessage";
                addConversationMessageReaction: "addConversationMessageReaction";
                removeConversationMessageReaction: "removeConversationMessageReaction";
            }>;
            args: z.ZodArray<z.ZodJSONSchema>;
        }, z.core.$strict>;
        readonly result: z.ZodUnion<[z.ZodJSONSchema, z.ZodUndefined]>;
    };
    readonly 'social.openNode': {
        readonly params: z.ZodObject<{
            spaceId: z.ZodString;
            nodeId: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodVoid;
    };
    readonly 'social.read': {
        readonly params: z.ZodObject<{
            operation: z.ZodEnum<{
                memberAvatar: "memberAvatar";
                attachmentContent: "attachmentContent";
            }>;
            spaceId: z.ZodString;
            id: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            bytes: z.ZodCustom<ArrayBuffer, ArrayBuffer>;
            mimeType: z.ZodString;
        }, z.core.$strict>;
    };
};
export type MistySocialOperation = typeof socialOperations[number];
export type MistySocialMethod = keyof typeof mistySocialContracts;
export declare const isMistySocialMethod: (method: string) => method is MistySocialMethod;
