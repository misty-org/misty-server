import { z } from 'zod';
export const agentOperations = ["agents.run", "agents.cancelRun", "agents.retryRun", "agents.decideApproval", "assistant.search", "assistant.visualSearch", "assistant.conversations", "assistant.createConversation", "assistant.deleteConversation", "assistant.renameConversation", "assistant.bindConversationSpace", "assistant.updateConversationSettings", "assistant.frontierModels", "assistant.turn", "assistant.complete", "assistant.decideProposal", "automations.flows", "automations.callTool", "mcp.list", "mcp.add", "mcp.test", "mcp.discover", "mcp.tools", "mcp.remove", "ai.status", "ai.usage", "ai.settings", "ai.updateSettings", "ai.memories", "ai.forgetMemory", "ai.conversations", "ai.conversation", "ai.updatePreference", "ai.recordProactiveEvent", "ai.recaps", "ai.updateRecap", "ai.markRecapSeen", "ai.feedback", "ai.createInvocation", "ai.createRun", "ai.cancelInvocation", "ai.decideArtifact", "ai.completeArtifact"];
const bytes = z.instanceof(ArrayBuffer).refine(v => v.byteLength <= 20 * 1024 * 1024);
export const mistyAgentsContracts = {
    'agents.perform': { params: z.strictObject({ operation: z.enum(agentOperations), args: z.array(z.json()).max(12) }), result: z.json().or(z.undefined()) },
    'agents.spaces': { params: z.strictObject({}), result: z.array(z.json()) },
    'agents.avatar': { params: z.strictObject({}), result: z.strictObject({ bytes, mimeType: z.string().max(255) }) },
    'agents.read': { params: z.strictObject({ attachmentId: z.string().min(1).max(256) }), result: z.strictObject({ bytes, mimeType: z.string().max(255) }) },
    'agents.uploadImage': { params: z.strictObject({ bytes, name: z.string().min(1).max(1024), mimeType: z.enum(['image/jpeg', 'image/png', 'image/webp']), conversationId: z.string().max(256).optional(), scope: z.enum(['conversation', 'visual_query']) }), result: z.json() },
    'agents.deleteImage': { params: z.strictObject({ id: z.string().min(1).max(256) }), result: z.void() },
    'agents.transcribe': { params: z.strictObject({ bytes, mimeType: z.string().max(255), durationMs: z.number().min(0).max(600000) }), result: z.strictObject({ transcript: z.string(), detected_language: z.string(), duration_ms: z.number() }) },
    'agents.research': { params: z.strictObject({ prompt: z.string().max(10000) }), result: z.json().nullable() },
};
export const isMistyAgentsMethod = (method) => Object.hasOwn(mistyAgentsContracts, method);
//# sourceMappingURL=agents.js.map