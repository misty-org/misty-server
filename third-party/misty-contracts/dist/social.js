import { z } from 'zod';
export const socialOperations = ["messages", "sendMessage", "runDetail", "decideRun", "cancelRun", "retryRun", "updateMessage", "deleteMessage", "addMessageReaction", "removeMessageReaction", "markRead", "conversations", "createConversation", "updateConversation", "deleteDisconnectedConversation", "deleteOrClearConversation", "clearEveryoneConversation", "markConversationRead", "conversationMessages", "sendConversationMessage", "updateConversationMessage", "deleteConversationMessage", "addConversationMessageReaction", "removeConversationMessageReaction", "members", "roadmaps", "nodes"];
export const mistySocialContracts = {
    'social.perform': { params: z.strictObject({ operation: z.enum(socialOperations), args: z.array(z.json()).max(12) }), result: z.json().or(z.undefined()) },
    'social.openNode': { params: z.strictObject({ spaceId: z.string().min(1).max(256), nodeId: z.string().min(1).max(256) }), result: z.void() },
    'social.read': { params: z.strictObject({ operation: z.enum(['memberAvatar', 'attachmentContent']), spaceId: z.string().min(1).max(256), id: z.string().min(1).max(256) }), result: z.strictObject({ bytes: z.instanceof(ArrayBuffer).refine(v => v.byteLength <= 128 * 1024 * 1024), mimeType: z.string().max(255) }) }
};
export const isMistySocialMethod = (method) => Object.hasOwn(mistySocialContracts, method);
//# sourceMappingURL=social.js.map