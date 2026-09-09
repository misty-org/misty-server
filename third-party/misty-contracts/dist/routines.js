import { z } from "zod";
import { MistyCapabilityInvocationSchema, MistyCapabilityJsonSchema, MistyCapabilityValueSchema } from "./capabilities.js";
const stepId = z.string().regex(/^[a-z][a-z0-9_]{0,63}$/);
const safeKey = z.string().min(1).max(200).refine(key => !["__proto__", "prototype", "constructor"].includes(key));
// String segments select object keys; numeric segments select array indexes.
const path = z.array(z.union([safeKey, z.number().int().min(0).max(100000)])).max(32);
export const MistyRoutineReferenceSchema = z.strictObject({
    kind: z.literal("reference"), source: z.discriminatedUnion("kind", [
        z.strictObject({ kind: z.literal("trigger") }),
        z.strictObject({ kind: z.literal("step"), stepId }),
    ]), path,
});
const value = z.lazy(() => z.discriminatedUnion("kind", [
    z.strictObject({ kind: z.literal("literal"), value: MistyCapabilityValueSchema }),
    MistyRoutineReferenceSchema,
    z.strictObject({ kind: z.literal("object"), fields: z.record(safeKey, value).refine(fields => Object.keys(fields).length <= 100) }),
    z.strictObject({ kind: z.literal("array"), items: z.array(value).max(100) }),
]));
const condition = z.lazy(() => z.discriminatedUnion("kind", [
    z.strictObject({ kind: z.literal("equals"), left: value, right: value }),
    z.strictObject({ kind: z.literal("exists"), reference: MistyRoutineReferenceSchema }),
    z.strictObject({ kind: z.enum(["all", "any"]), conditions: z.array(condition).min(1).max(20) }),
    z.strictObject({ kind: z.literal("not"), condition }),
]));
// Bound recursion before entering recursive Zod schemas, including literal data.
function boundedTree(input, ctx) {
    const seen = new Set();
    let nodes = 0;
    const valid = (item, depth) => {
        if (++nodes > 20000 || depth > 32)
            return false;
        if (!item || typeof item !== "object")
            return true;
        if (seen.has(item))
            return false;
        seen.add(item);
        const okay = Object.entries(item).every(([key, child]) => !["__proto__", "prototype", "constructor"].includes(key) && valid(child, depth + 1));
        seen.delete(item);
        return okay;
    };
    if (!valid(input, 0)) {
        ctx.addIssue({ code: "custom", message: "Routine data is unsafe or too deeply nested." });
        return z.NEVER;
    }
    try {
        if (new TextEncoder().encode(JSON.stringify(input)).length > 1024 * 1024)
            throw new Error();
    }
    catch {
        ctx.addIssue({ code: "custom", message: "Routine data must be bounded JSON." });
        return z.NEVER;
    }
    return input;
}
export const MistyRoutineValueSchema = z.preprocess(boundedTree, value);
export const MistyRoutineConditionSchema = z.preprocess(boundedTree, condition);
export const MistyRoutineCapabilityPinSchema = MistyCapabilityInvocationSchema.pick({
    capability: true, capabilityVersion: true, providerId: true, providerVersion: true, targetId: true, targetRevision: true,
});
const base = { id: stepId, label: z.string().min(1).max(200), when: condition.optional() };
const stepDefinition = z.discriminatedUnion("kind", [
    z.strictObject({ ...base, kind: z.literal("capability"), action: MistyRoutineCapabilityPinSchema, input: value, allowPartial: z.boolean().default(false) }),
    z.strictObject({ ...base, kind: z.literal("agent"), prompt: value, actions: z.array(MistyRoutineCapabilityPinSchema).min(1).max(20), maxTurns: z.number().int().min(1).max(20), outputSchema: MistyCapabilityJsonSchema }),
    z.strictObject({ ...base, kind: z.literal("wait"), until: value }),
]);
export const MistyRoutineStepSchema = z.preprocess(boundedTree, stepDefinition);
export const MistyRoutineTriggerSchema = z.discriminatedUnion("kind", [
    z.strictObject({ kind: z.literal("manual") }),
    z.strictObject({ kind: z.literal("schedule"), timezone: z.string().min(1).max(100).refine(zone => { try {
            new Intl.DateTimeFormat("en", { timeZone: zone });
            return true;
        }
        catch {
            return false;
        } }),
        daysOfWeek: z.array(z.number().int().min(0).max(6)).min(1).max(7).refine(items => new Set(items).size === items.length),
        times: z.array(z.strictObject({ hour: z.number().int().min(0).max(23), minute: z.number().int().min(0).max(59) })).min(1).max(24).refine(items => new Set(items.map(item => `${item.hour}:${item.minute}`)).size === items.length),
    }),
    z.strictObject({ kind: z.literal("app_event"), event: z.enum(["planner.task.changed", "journal.note.changed"]), resourceIds: z.array(z.string().min(1).max(256)).max(100).default([]) }),
]);
const definition = z.strictObject({
    protocol: z.literal(1), name: z.string().min(1).max(200), description: z.string().max(2000).default(""),
    spaceId: z.string().regex(/^[A-Za-z0-9_-]{1,256}$/).optional(), trigger: MistyRoutineTriggerSchema,
    steps: z.array(stepDefinition).min(1).max(50),
    budget: z.strictObject({ modelTurns: z.number().int().min(1).max(20).default(20), activeSeconds: z.number().int().min(1).max(1800).default(1800) }),
});
export const MistyRoutineDefinitionSchema = z.preprocess(boundedTree, definition).superRefine((routine, ctx) => {
    const seen = new Set();
    let turns = 0;
    const references = (input, path) => {
        if (!input || typeof input !== "object")
            return;
        const item = input;
        // Literal content is data, even when it resembles a reference expression.
        if (item.kind === "literal")
            return;
        if (item.kind === "reference") {
            const ref = item;
            if (ref.source.kind === "step" && !seen.has(ref.source.stepId))
                ctx.addIssue({ code: "custom", path, message: "A reference must name an earlier step." });
            return;
        }
        for (const [key, child] of Object.entries(item))
            references(child, [...path, key]);
    };
    routine.steps.forEach((step, index) => {
        if (seen.has(step.id))
            ctx.addIssue({ code: "custom", path: ["steps", index, "id"], message: "Step identities must be unique." });
        references(step.when, ["steps", index, "when"]);
        references(step.kind === "capability" ? step.input : step.kind === "agent" ? step.prompt : step.until, ["steps", index]);
        if (step.kind === "agent")
            turns += step.maxTurns;
        seen.add(step.id);
    });
    if (turns > routine.budget.modelTurns)
        ctx.addIssue({ code: "custom", path: ["budget", "modelTurns"], message: "Agent steps exceed the routine's total model-turn budget." });
});
export const MistyRoutineAgentBindingSchema = z.strictObject({
    stepId, callNamespace: z.string().uuid(),
    tools: z.array(z.strictObject({ toolName: z.string().min(1).max(240), action: MistyRoutineCapabilityPinSchema })).min(1).max(20),
});
/** Compiled by Go at admission. App/agent input cannot choose execution call IDs. */
const executionDefinition = z.strictObject({
    routineId: z.string().uuid(), version: z.number().int().positive().max(2147483647),
    runId: z.string().min(1).max(256), definition: MistyRoutineDefinitionSchema,
    trigger: MistyCapabilityValueSchema,
    bindings: z.array(z.strictObject({ stepId, callId: z.string().uuid(), toolName: z.string().min(1).max(240) })).max(50),
    agentBindings: z.array(MistyRoutineAgentBindingSchema).max(20).default([]),
}).superRefine((execution, ctx) => {
    const steps = execution.definition.steps.filter(step => step.kind === "capability");
    const ids = execution.bindings.map(binding => binding.stepId);
    if (new Set(ids).size !== ids.length || new Set(execution.bindings.map(binding => binding.callId)).size !== ids.length || ids.length !== steps.length || steps.some(step => !ids.includes(step.id)))
        ctx.addIssue({ code: "custom", path: ["bindings"], message: "Each capability step needs one unique admitted call identity." });
    const agents = execution.definition.steps.filter(step => step.kind === "agent");
    const agentIds = execution.agentBindings.map(binding => binding.stepId);
    if (new Set(agentIds).size !== agentIds.length || agentIds.length !== agents.length || new Set(execution.agentBindings.map(binding => binding.callNamespace)).size !== agentIds.length)
        ctx.addIssue({ code: "custom", path: ["agentBindings"], message: "Each agent step needs one unique admitted call namespace." });
    for (const agent of agents) {
        const binding = execution.agentBindings.find(binding => binding.stepId === agent.id);
        const expected = new Set(agent.actions.map(action => JSON.stringify(action)));
        if (!binding || binding.tools.length !== expected.size || new Set(binding.tools.map(tool => tool.toolName)).size !== binding.tools.length || new Set(binding.tools.map(tool => JSON.stringify(tool.action))).size !== expected.size || binding.tools.some(tool => !expected.has(JSON.stringify(tool.action))))
            ctx.addIssue({ code: "custom", path: ["agentBindings"], message: "Agent tools must match exactly the step's pinned actions." });
    }
});
export const MistyRoutineExecutionSchema = z.preprocess(boundedTree, executionDefinition);
/** Trusted Misty authoring controls, deliberately excluded from app RPC. */
export const MistyRoutineDraftSaveSchema = z.strictObject({
    expectedVersion: z.number().int().min(0).max(2147483646),
    definition: MistyRoutineDefinitionSchema,
});
export const MistyRoutineDraftRecordSchema = z.strictObject({
    routineId: z.string().uuid(), version: z.number().int().positive(), currentVersion: z.number().int().positive(),
    state: z.literal("draft"), definition: MistyRoutineDefinitionSchema,
    createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }),
});
export const MistyRoutineDraftSummarySchema = z.strictObject({
    routineId: z.string().uuid(), version: z.number().int().positive(), name: z.string().min(1).max(200),
    description: z.string().max(2000), state: z.literal("draft"), updatedAt: z.iso.datetime({ offset: true }),
});
export const MistyRoutineDraftPageSchema = z.strictObject({
    routines: z.array(MistyRoutineDraftSummarySchema).max(100), nextCursor: z.string().uuid().optional(),
});
export const MistyRoutineManualRunSchema = z.strictObject({
    requestId: z.string().uuid(), version: z.number().int().positive().max(2147483647),
    trigger: MistyCapabilityValueSchema.default({}),
});
export const MistyRoutineReportSchema = z.strictObject({
    state: z.enum(["completed", "partial", "failed", "uncertain", "cancelled"]),
    steps: z.array(z.strictObject({ stepId, state: z.enum(["completed", "partial", "skipped", "failed", "uncertain", "not_run"]), callId: z.string().uuid().optional() })).max(50),
});
export const MistyRoutineRunRecordSchema = z.strictObject({
    requestId: z.string().uuid(), execution: MistyRoutineExecutionSchema,
    state: z.enum(["queued", "running", "awaiting_approval", "awaiting_device", "awaiting_intervention", "awaiting_timer", "completed", "failed", "canceled"]),
    cancelRequested: z.boolean(), outcome: z.enum(["", "completed", "partial", "failed", "uncertain", "cancelled"]),
    report: MistyRoutineReportSchema.nullable(),
    wait: z.strictObject({ waitId: z.string().uuid(), stepId, until: z.iso.datetime({ offset: true }), expiresAt: z.iso.datetime({ offset: true }) }).optional(),
});
//# sourceMappingURL=routines.js.map