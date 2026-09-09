import { z } from "zod";
import { MistyCapabilityDefinitionSchema, MistyCapabilityEvidenceSchema } from "./capabilities.js";
/** A resolved container, never a freeform provider name or a request to guess. */
export const MistyTaskDestinationSchema = z.strictObject({
    targetId: z.uuid(), containerReference: z.string().min(1).max(2048),
    label: z.string().min(1).max(300),
});
export const MistyTaskCreateInputSchema = z.strictObject({
    title: z.string().min(1).max(240).regex(/^\S(?:[\s\S]*\S)?$/), text: z.string().max(20000).regex(/^(?:|\S(?:[\s\S]*\S)?)$/),
    destination: MistyTaskDestinationSchema,
    dueDate: z.iso.date().optional(),
    source: z.strictObject({ reference: z.string().min(1).max(2048), label: z.string().min(1).max(300) }),
});
export const MistyTaskCreateResultSchema = z.strictObject({
    taskReference: z.string().min(1).max(2048), destination: MistyTaskDestinationSchema,
    title: z.string().min(1).max(240).regex(/^\S(?:[\s\S]*\S)?$/), text: z.string().max(20000).regex(/^(?:|\S(?:[\s\S]*\S)?)$/), dueDate: z.iso.date().optional(),
    source: MistyTaskCreateInputSchema.shape.source,
    evidence: z.array(MistyCapabilityEvidenceSchema).min(1).max(100),
});
export const mistyTaskCapabilities = [MistyCapabilityDefinitionSchema.parse({
        name: "tasks.create", version: 1,
        description: "Create one task in an explicitly resolved container and verify its content, destination, source and identity. Unqualified requests visibly default to the originating Space Planner; missing Space or ambiguous external accounts/projects require clarification. Reconcile an uncertain creation before any retry.",
        inputSchema: z.toJSONSchema(MistyTaskCreateInputSchema), outputSchema: z.toJSONSchema(MistyTaskCreateResultSchema),
        requiredScopes: ["tasks.create"],
        effects: { kind: "write", approval: "interactive", retry: "reconcile", incidental: [] },
    })];
//# sourceMappingURL=task-capabilities.js.map