import { z } from 'zod';
export declare const agentOperations: readonly ["agents.run", "agents.cancelRun", "agents.retryRun", "agents.decideApproval", "assistant.search", "assistant.visualSearch", "assistant.conversations", "assistant.createConversation", "assistant.deleteConversation", "assistant.renameConversation", "assistant.bindConversationSpace", "assistant.updateConversationSettings", "assistant.frontierModels", "assistant.turn", "assistant.complete", "assistant.decideProposal", "automations.flows", "automations.callTool", "mcp.list", "mcp.add", "mcp.test", "mcp.discover", "mcp.tools", "mcp.remove", "ai.status", "ai.usage", "ai.settings", "ai.updateSettings", "ai.memories", "ai.forgetMemory", "ai.conversations", "ai.conversation", "ai.updatePreference", "ai.recordProactiveEvent", "ai.recaps", "ai.updateRecap", "ai.markRecapSeen", "ai.feedback", "ai.createInvocation", "ai.createRun", "ai.cancelInvocation", "ai.decideArtifact", "ai.completeArtifact"];
export declare const mistyAgentsContracts: {
    readonly 'agents.perform': {
        readonly params: z.ZodObject<{
            operation: z.ZodEnum<{
                "agents.run": "agents.run";
                "agents.cancelRun": "agents.cancelRun";
                "agents.retryRun": "agents.retryRun";
                "agents.decideApproval": "agents.decideApproval";
                "assistant.search": "assistant.search";
                "assistant.visualSearch": "assistant.visualSearch";
                "assistant.conversations": "assistant.conversations";
                "assistant.createConversation": "assistant.createConversation";
                "assistant.deleteConversation": "assistant.deleteConversation";
                "assistant.renameConversation": "assistant.renameConversation";
                "assistant.bindConversationSpace": "assistant.bindConversationSpace";
                "assistant.updateConversationSettings": "assistant.updateConversationSettings";
                "assistant.frontierModels": "assistant.frontierModels";
                "assistant.turn": "assistant.turn";
                "assistant.complete": "assistant.complete";
                "assistant.decideProposal": "assistant.decideProposal";
                "automations.flows": "automations.flows";
                "automations.callTool": "automations.callTool";
                "mcp.list": "mcp.list";
                "mcp.add": "mcp.add";
                "mcp.test": "mcp.test";
                "mcp.discover": "mcp.discover";
                "mcp.tools": "mcp.tools";
                "mcp.remove": "mcp.remove";
                "ai.status": "ai.status";
                "ai.usage": "ai.usage";
                "ai.settings": "ai.settings";
                "ai.updateSettings": "ai.updateSettings";
                "ai.memories": "ai.memories";
                "ai.forgetMemory": "ai.forgetMemory";
                "ai.conversations": "ai.conversations";
                "ai.conversation": "ai.conversation";
                "ai.updatePreference": "ai.updatePreference";
                "ai.recordProactiveEvent": "ai.recordProactiveEvent";
                "ai.recaps": "ai.recaps";
                "ai.updateRecap": "ai.updateRecap";
                "ai.markRecapSeen": "ai.markRecapSeen";
                "ai.feedback": "ai.feedback";
                "ai.createInvocation": "ai.createInvocation";
                "ai.createRun": "ai.createRun";
                "ai.cancelInvocation": "ai.cancelInvocation";
                "ai.decideArtifact": "ai.decideArtifact";
                "ai.completeArtifact": "ai.completeArtifact";
            }>;
            args: z.ZodArray<z.ZodJSONSchema>;
        }, z.core.$strict>;
        readonly result: z.ZodUnion<[z.ZodJSONSchema, z.ZodUndefined]>;
    };
    readonly 'agents.spaces': {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodArray<z.ZodJSONSchema>;
    };
    readonly 'agents.avatar': {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodObject<{
            bytes: z.ZodCustom<ArrayBuffer, ArrayBuffer>;
            mimeType: z.ZodString;
        }, z.core.$strict>;
    };
    readonly 'agents.read': {
        readonly params: z.ZodObject<{
            attachmentId: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            bytes: z.ZodCustom<ArrayBuffer, ArrayBuffer>;
            mimeType: z.ZodString;
        }, z.core.$strict>;
    };
    readonly 'agents.uploadImage': {
        readonly params: z.ZodObject<{
            bytes: z.ZodCustom<ArrayBuffer, ArrayBuffer>;
            name: z.ZodString;
            mimeType: z.ZodEnum<{
                "image/jpeg": "image/jpeg";
                "image/png": "image/png";
                "image/webp": "image/webp";
            }>;
            conversationId: z.ZodOptional<z.ZodString>;
            scope: z.ZodEnum<{
                conversation: "conversation";
                visual_query: "visual_query";
            }>;
        }, z.core.$strict>;
        readonly result: z.ZodJSONSchema;
    };
    readonly 'agents.deleteImage': {
        readonly params: z.ZodObject<{
            id: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodVoid;
    };
    readonly 'agents.transcribe': {
        readonly params: z.ZodObject<{
            bytes: z.ZodCustom<ArrayBuffer, ArrayBuffer>;
            mimeType: z.ZodString;
            durationMs: z.ZodNumber;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            transcript: z.ZodString;
            detected_language: z.ZodString;
            duration_ms: z.ZodNumber;
        }, z.core.$strict>;
    };
    readonly 'agents.research': {
        readonly params: z.ZodObject<{
            prompt: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodNullable<z.ZodJSONSchema>;
    };
};
export type MistyAgentOperation = typeof agentOperations[number];
export type MistyAgentsMethod = keyof typeof mistyAgentsContracts;
export declare const isMistyAgentsMethod: (method: string) => method is MistyAgentsMethod;
