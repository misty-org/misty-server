import { z } from "zod";
import { MistyCapabilityValueSchema } from "./capabilities.js";
export declare const MistyRoutineReferenceSchema: z.ZodObject<{
    kind: z.ZodLiteral<"reference">;
    source: z.ZodDiscriminatedUnion<[z.ZodObject<{
        kind: z.ZodLiteral<"trigger">;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"step">;
        stepId: z.ZodString;
    }, z.core.$strict>], "kind">;
    path: z.ZodArray<z.ZodUnion<readonly [z.ZodString, z.ZodNumber]>>;
}, z.core.$strict>;
export type MistyRoutineReference = z.output<typeof MistyRoutineReferenceSchema>;
export type MistyRoutineValue = {
    kind: "literal";
    value: z.output<typeof MistyCapabilityValueSchema>;
} | MistyRoutineReference | {
    kind: "object";
    fields: Record<string, MistyRoutineValue>;
} | {
    kind: "array";
    items: MistyRoutineValue[];
};
export type MistyRoutineCondition = {
    kind: "equals";
    left: MistyRoutineValue;
    right: MistyRoutineValue;
} | {
    kind: "exists";
    reference: MistyRoutineReference;
} | {
    kind: "all" | "any";
    conditions: MistyRoutineCondition[];
} | {
    kind: "not";
    condition: MistyRoutineCondition;
};
export declare const MistyRoutineValueSchema: z.ZodPreprocess<z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>>;
export declare const MistyRoutineConditionSchema: z.ZodPreprocess<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
export declare const MistyRoutineCapabilityPinSchema: z.ZodObject<{
    providerId: z.ZodString;
    capability: z.ZodString;
    targetId: z.ZodString;
    providerVersion: z.ZodNumber;
    capabilityVersion: z.ZodNumber;
    targetRevision: z.ZodNumber;
}, z.core.$strict>;
export declare const MistyRoutineStepSchema: z.ZodPreprocess<z.ZodDiscriminatedUnion<[z.ZodObject<{
    kind: z.ZodLiteral<"capability">;
    action: z.ZodObject<{
        providerId: z.ZodString;
        capability: z.ZodString;
        targetId: z.ZodString;
        providerVersion: z.ZodNumber;
        capabilityVersion: z.ZodNumber;
        targetRevision: z.ZodNumber;
    }, z.core.$strict>;
    input: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
    allowPartial: z.ZodDefault<z.ZodBoolean>;
    id: z.ZodString;
    label: z.ZodString;
    when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"agent">;
    prompt: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
    actions: z.ZodArray<z.ZodObject<{
        providerId: z.ZodString;
        capability: z.ZodString;
        targetId: z.ZodString;
        providerVersion: z.ZodNumber;
        capabilityVersion: z.ZodNumber;
        targetRevision: z.ZodNumber;
    }, z.core.$strict>>;
    maxTurns: z.ZodNumber;
    outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
    id: z.ZodString;
    label: z.ZodString;
    when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"wait">;
    until: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
    id: z.ZodString;
    label: z.ZodString;
    when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
}, z.core.$strict>], "kind">>;
export declare const MistyRoutineTriggerSchema: z.ZodDiscriminatedUnion<[z.ZodObject<{
    kind: z.ZodLiteral<"manual">;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"schedule">;
    timezone: z.ZodString;
    daysOfWeek: z.ZodArray<z.ZodNumber>;
    times: z.ZodArray<z.ZodObject<{
        hour: z.ZodNumber;
        minute: z.ZodNumber;
    }, z.core.$strict>>;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"app_event">;
    event: z.ZodEnum<{
        "planner.task.changed": "planner.task.changed";
        "journal.note.changed": "journal.note.changed";
    }>;
    resourceIds: z.ZodDefault<z.ZodArray<z.ZodString>>;
}, z.core.$strict>], "kind">;
export declare const MistyRoutineDefinitionSchema: z.ZodPreprocess<z.ZodObject<{
    protocol: z.ZodLiteral<1>;
    name: z.ZodString;
    description: z.ZodDefault<z.ZodString>;
    spaceId: z.ZodOptional<z.ZodString>;
    trigger: z.ZodDiscriminatedUnion<[z.ZodObject<{
        kind: z.ZodLiteral<"manual">;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"schedule">;
        timezone: z.ZodString;
        daysOfWeek: z.ZodArray<z.ZodNumber>;
        times: z.ZodArray<z.ZodObject<{
            hour: z.ZodNumber;
            minute: z.ZodNumber;
        }, z.core.$strict>>;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"app_event">;
        event: z.ZodEnum<{
            "planner.task.changed": "planner.task.changed";
            "journal.note.changed": "journal.note.changed";
        }>;
        resourceIds: z.ZodDefault<z.ZodArray<z.ZodString>>;
    }, z.core.$strict>], "kind">;
    steps: z.ZodArray<z.ZodDiscriminatedUnion<[z.ZodObject<{
        kind: z.ZodLiteral<"capability">;
        action: z.ZodObject<{
            providerId: z.ZodString;
            capability: z.ZodString;
            targetId: z.ZodString;
            providerVersion: z.ZodNumber;
            capabilityVersion: z.ZodNumber;
            targetRevision: z.ZodNumber;
        }, z.core.$strict>;
        input: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
        allowPartial: z.ZodDefault<z.ZodBoolean>;
        id: z.ZodString;
        label: z.ZodString;
        when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"agent">;
        prompt: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
        actions: z.ZodArray<z.ZodObject<{
            providerId: z.ZodString;
            capability: z.ZodString;
            targetId: z.ZodString;
            providerVersion: z.ZodNumber;
            capabilityVersion: z.ZodNumber;
            targetRevision: z.ZodNumber;
        }, z.core.$strict>>;
        maxTurns: z.ZodNumber;
        outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
        id: z.ZodString;
        label: z.ZodString;
        when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"wait">;
        until: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
        id: z.ZodString;
        label: z.ZodString;
        when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
    }, z.core.$strict>], "kind">>;
    budget: z.ZodObject<{
        modelTurns: z.ZodDefault<z.ZodNumber>;
        activeSeconds: z.ZodDefault<z.ZodNumber>;
    }, z.core.$strict>;
}, z.core.$strict>>;
export type MistyRoutineDefinition = z.output<typeof MistyRoutineDefinitionSchema>;
export type MistyRoutineStep = z.output<typeof MistyRoutineStepSchema>;
export declare const MistyRoutineAgentBindingSchema: z.ZodObject<{
    stepId: z.ZodString;
    callNamespace: z.ZodString;
    tools: z.ZodArray<z.ZodObject<{
        toolName: z.ZodString;
        action: z.ZodObject<{
            providerId: z.ZodString;
            capability: z.ZodString;
            targetId: z.ZodString;
            providerVersion: z.ZodNumber;
            capabilityVersion: z.ZodNumber;
            targetRevision: z.ZodNumber;
        }, z.core.$strict>;
    }, z.core.$strict>>;
}, z.core.$strict>;
export type MistyRoutineAgentBinding = z.output<typeof MistyRoutineAgentBindingSchema>;
export declare const MistyRoutineExecutionSchema: z.ZodPreprocess<z.ZodObject<{
    routineId: z.ZodString;
    version: z.ZodNumber;
    runId: z.ZodString;
    definition: z.ZodPreprocess<z.ZodObject<{
        protocol: z.ZodLiteral<1>;
        name: z.ZodString;
        description: z.ZodDefault<z.ZodString>;
        spaceId: z.ZodOptional<z.ZodString>;
        trigger: z.ZodDiscriminatedUnion<[z.ZodObject<{
            kind: z.ZodLiteral<"manual">;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"schedule">;
            timezone: z.ZodString;
            daysOfWeek: z.ZodArray<z.ZodNumber>;
            times: z.ZodArray<z.ZodObject<{
                hour: z.ZodNumber;
                minute: z.ZodNumber;
            }, z.core.$strict>>;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"app_event">;
            event: z.ZodEnum<{
                "planner.task.changed": "planner.task.changed";
                "journal.note.changed": "journal.note.changed";
            }>;
            resourceIds: z.ZodDefault<z.ZodArray<z.ZodString>>;
        }, z.core.$strict>], "kind">;
        steps: z.ZodArray<z.ZodDiscriminatedUnion<[z.ZodObject<{
            kind: z.ZodLiteral<"capability">;
            action: z.ZodObject<{
                providerId: z.ZodString;
                capability: z.ZodString;
                targetId: z.ZodString;
                providerVersion: z.ZodNumber;
                capabilityVersion: z.ZodNumber;
                targetRevision: z.ZodNumber;
            }, z.core.$strict>;
            input: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
            allowPartial: z.ZodDefault<z.ZodBoolean>;
            id: z.ZodString;
            label: z.ZodString;
            when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"agent">;
            prompt: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
            actions: z.ZodArray<z.ZodObject<{
                providerId: z.ZodString;
                capability: z.ZodString;
                targetId: z.ZodString;
                providerVersion: z.ZodNumber;
                capabilityVersion: z.ZodNumber;
                targetRevision: z.ZodNumber;
            }, z.core.$strict>>;
            maxTurns: z.ZodNumber;
            outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
            id: z.ZodString;
            label: z.ZodString;
            when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"wait">;
            until: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
            id: z.ZodString;
            label: z.ZodString;
            when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
        }, z.core.$strict>], "kind">>;
        budget: z.ZodObject<{
            modelTurns: z.ZodDefault<z.ZodNumber>;
            activeSeconds: z.ZodDefault<z.ZodNumber>;
        }, z.core.$strict>;
    }, z.core.$strict>>;
    trigger: z.ZodJSONSchema;
    bindings: z.ZodArray<z.ZodObject<{
        stepId: z.ZodString;
        callId: z.ZodString;
        toolName: z.ZodString;
    }, z.core.$strict>>;
    agentBindings: z.ZodDefault<z.ZodArray<z.ZodObject<{
        stepId: z.ZodString;
        callNamespace: z.ZodString;
        tools: z.ZodArray<z.ZodObject<{
            toolName: z.ZodString;
            action: z.ZodObject<{
                providerId: z.ZodString;
                capability: z.ZodString;
                targetId: z.ZodString;
                providerVersion: z.ZodNumber;
                capabilityVersion: z.ZodNumber;
                targetRevision: z.ZodNumber;
            }, z.core.$strict>;
        }, z.core.$strict>>;
    }, z.core.$strict>>>;
}, z.core.$strict>>;
export type MistyRoutineExecution = z.output<typeof MistyRoutineExecutionSchema>;
/** Trusted Misty authoring controls, deliberately excluded from app RPC. */
export declare const MistyRoutineDraftSaveSchema: z.ZodObject<{
    expectedVersion: z.ZodNumber;
    definition: z.ZodPreprocess<z.ZodObject<{
        protocol: z.ZodLiteral<1>;
        name: z.ZodString;
        description: z.ZodDefault<z.ZodString>;
        spaceId: z.ZodOptional<z.ZodString>;
        trigger: z.ZodDiscriminatedUnion<[z.ZodObject<{
            kind: z.ZodLiteral<"manual">;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"schedule">;
            timezone: z.ZodString;
            daysOfWeek: z.ZodArray<z.ZodNumber>;
            times: z.ZodArray<z.ZodObject<{
                hour: z.ZodNumber;
                minute: z.ZodNumber;
            }, z.core.$strict>>;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"app_event">;
            event: z.ZodEnum<{
                "planner.task.changed": "planner.task.changed";
                "journal.note.changed": "journal.note.changed";
            }>;
            resourceIds: z.ZodDefault<z.ZodArray<z.ZodString>>;
        }, z.core.$strict>], "kind">;
        steps: z.ZodArray<z.ZodDiscriminatedUnion<[z.ZodObject<{
            kind: z.ZodLiteral<"capability">;
            action: z.ZodObject<{
                providerId: z.ZodString;
                capability: z.ZodString;
                targetId: z.ZodString;
                providerVersion: z.ZodNumber;
                capabilityVersion: z.ZodNumber;
                targetRevision: z.ZodNumber;
            }, z.core.$strict>;
            input: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
            allowPartial: z.ZodDefault<z.ZodBoolean>;
            id: z.ZodString;
            label: z.ZodString;
            when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"agent">;
            prompt: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
            actions: z.ZodArray<z.ZodObject<{
                providerId: z.ZodString;
                capability: z.ZodString;
                targetId: z.ZodString;
                providerVersion: z.ZodNumber;
                capabilityVersion: z.ZodNumber;
                targetRevision: z.ZodNumber;
            }, z.core.$strict>>;
            maxTurns: z.ZodNumber;
            outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
            id: z.ZodString;
            label: z.ZodString;
            when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"wait">;
            until: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
            id: z.ZodString;
            label: z.ZodString;
            when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
        }, z.core.$strict>], "kind">>;
        budget: z.ZodObject<{
            modelTurns: z.ZodDefault<z.ZodNumber>;
            activeSeconds: z.ZodDefault<z.ZodNumber>;
        }, z.core.$strict>;
    }, z.core.$strict>>;
}, z.core.$strict>;
export declare const MistyRoutineDraftRecordSchema: z.ZodObject<{
    routineId: z.ZodString;
    version: z.ZodNumber;
    currentVersion: z.ZodNumber;
    state: z.ZodLiteral<"draft">;
    definition: z.ZodPreprocess<z.ZodObject<{
        protocol: z.ZodLiteral<1>;
        name: z.ZodString;
        description: z.ZodDefault<z.ZodString>;
        spaceId: z.ZodOptional<z.ZodString>;
        trigger: z.ZodDiscriminatedUnion<[z.ZodObject<{
            kind: z.ZodLiteral<"manual">;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"schedule">;
            timezone: z.ZodString;
            daysOfWeek: z.ZodArray<z.ZodNumber>;
            times: z.ZodArray<z.ZodObject<{
                hour: z.ZodNumber;
                minute: z.ZodNumber;
            }, z.core.$strict>>;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"app_event">;
            event: z.ZodEnum<{
                "planner.task.changed": "planner.task.changed";
                "journal.note.changed": "journal.note.changed";
            }>;
            resourceIds: z.ZodDefault<z.ZodArray<z.ZodString>>;
        }, z.core.$strict>], "kind">;
        steps: z.ZodArray<z.ZodDiscriminatedUnion<[z.ZodObject<{
            kind: z.ZodLiteral<"capability">;
            action: z.ZodObject<{
                providerId: z.ZodString;
                capability: z.ZodString;
                targetId: z.ZodString;
                providerVersion: z.ZodNumber;
                capabilityVersion: z.ZodNumber;
                targetRevision: z.ZodNumber;
            }, z.core.$strict>;
            input: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
            allowPartial: z.ZodDefault<z.ZodBoolean>;
            id: z.ZodString;
            label: z.ZodString;
            when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"agent">;
            prompt: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
            actions: z.ZodArray<z.ZodObject<{
                providerId: z.ZodString;
                capability: z.ZodString;
                targetId: z.ZodString;
                providerVersion: z.ZodNumber;
                capabilityVersion: z.ZodNumber;
                targetRevision: z.ZodNumber;
            }, z.core.$strict>>;
            maxTurns: z.ZodNumber;
            outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
            id: z.ZodString;
            label: z.ZodString;
            when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"wait">;
            until: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
            id: z.ZodString;
            label: z.ZodString;
            when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
        }, z.core.$strict>], "kind">>;
        budget: z.ZodObject<{
            modelTurns: z.ZodDefault<z.ZodNumber>;
            activeSeconds: z.ZodDefault<z.ZodNumber>;
        }, z.core.$strict>;
    }, z.core.$strict>>;
    createdAt: z.ZodISODateTime;
    updatedAt: z.ZodISODateTime;
}, z.core.$strict>;
export declare const MistyRoutineDraftSummarySchema: z.ZodObject<{
    routineId: z.ZodString;
    version: z.ZodNumber;
    name: z.ZodString;
    description: z.ZodString;
    state: z.ZodLiteral<"draft">;
    updatedAt: z.ZodISODateTime;
}, z.core.$strict>;
export declare const MistyRoutineDraftPageSchema: z.ZodObject<{
    routines: z.ZodArray<z.ZodObject<{
        routineId: z.ZodString;
        version: z.ZodNumber;
        name: z.ZodString;
        description: z.ZodString;
        state: z.ZodLiteral<"draft">;
        updatedAt: z.ZodISODateTime;
    }, z.core.$strict>>;
    nextCursor: z.ZodOptional<z.ZodString>;
}, z.core.$strict>;
export type MistyRoutineDraftRecord = z.output<typeof MistyRoutineDraftRecordSchema>;
export type MistyRoutineDraftPage = z.output<typeof MistyRoutineDraftPageSchema>;
export declare const MistyRoutineManualRunSchema: z.ZodObject<{
    requestId: z.ZodString;
    version: z.ZodNumber;
    trigger: z.ZodDefault<z.ZodJSONSchema>;
}, z.core.$strict>;
export declare const MistyRoutineReportSchema: z.ZodObject<{
    state: z.ZodEnum<{
        failed: "failed";
        partial: "partial";
        uncertain: "uncertain";
        completed: "completed";
        cancelled: "cancelled";
    }>;
    steps: z.ZodArray<z.ZodObject<{
        stepId: z.ZodString;
        state: z.ZodEnum<{
            failed: "failed";
            partial: "partial";
            uncertain: "uncertain";
            completed: "completed";
            skipped: "skipped";
            not_run: "not_run";
        }>;
        callId: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
}, z.core.$strict>;
export declare const MistyRoutineRunRecordSchema: z.ZodObject<{
    requestId: z.ZodString;
    execution: z.ZodPreprocess<z.ZodObject<{
        routineId: z.ZodString;
        version: z.ZodNumber;
        runId: z.ZodString;
        definition: z.ZodPreprocess<z.ZodObject<{
            protocol: z.ZodLiteral<1>;
            name: z.ZodString;
            description: z.ZodDefault<z.ZodString>;
            spaceId: z.ZodOptional<z.ZodString>;
            trigger: z.ZodDiscriminatedUnion<[z.ZodObject<{
                kind: z.ZodLiteral<"manual">;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"schedule">;
                timezone: z.ZodString;
                daysOfWeek: z.ZodArray<z.ZodNumber>;
                times: z.ZodArray<z.ZodObject<{
                    hour: z.ZodNumber;
                    minute: z.ZodNumber;
                }, z.core.$strict>>;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"app_event">;
                event: z.ZodEnum<{
                    "planner.task.changed": "planner.task.changed";
                    "journal.note.changed": "journal.note.changed";
                }>;
                resourceIds: z.ZodDefault<z.ZodArray<z.ZodString>>;
            }, z.core.$strict>], "kind">;
            steps: z.ZodArray<z.ZodDiscriminatedUnion<[z.ZodObject<{
                kind: z.ZodLiteral<"capability">;
                action: z.ZodObject<{
                    providerId: z.ZodString;
                    capability: z.ZodString;
                    targetId: z.ZodString;
                    providerVersion: z.ZodNumber;
                    capabilityVersion: z.ZodNumber;
                    targetRevision: z.ZodNumber;
                }, z.core.$strict>;
                input: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
                allowPartial: z.ZodDefault<z.ZodBoolean>;
                id: z.ZodString;
                label: z.ZodString;
                when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"agent">;
                prompt: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
                actions: z.ZodArray<z.ZodObject<{
                    providerId: z.ZodString;
                    capability: z.ZodString;
                    targetId: z.ZodString;
                    providerVersion: z.ZodNumber;
                    capabilityVersion: z.ZodNumber;
                    targetRevision: z.ZodNumber;
                }, z.core.$strict>>;
                maxTurns: z.ZodNumber;
                outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
                id: z.ZodString;
                label: z.ZodString;
                when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"wait">;
                until: z.ZodType<MistyRoutineValue, unknown, z.core.$ZodTypeInternals<MistyRoutineValue, unknown>>;
                id: z.ZodString;
                label: z.ZodString;
                when: z.ZodOptional<z.ZodType<MistyRoutineCondition, unknown, z.core.$ZodTypeInternals<MistyRoutineCondition, unknown>>>;
            }, z.core.$strict>], "kind">>;
            budget: z.ZodObject<{
                modelTurns: z.ZodDefault<z.ZodNumber>;
                activeSeconds: z.ZodDefault<z.ZodNumber>;
            }, z.core.$strict>;
        }, z.core.$strict>>;
        trigger: z.ZodJSONSchema;
        bindings: z.ZodArray<z.ZodObject<{
            stepId: z.ZodString;
            callId: z.ZodString;
            toolName: z.ZodString;
        }, z.core.$strict>>;
        agentBindings: z.ZodDefault<z.ZodArray<z.ZodObject<{
            stepId: z.ZodString;
            callNamespace: z.ZodString;
            tools: z.ZodArray<z.ZodObject<{
                toolName: z.ZodString;
                action: z.ZodObject<{
                    providerId: z.ZodString;
                    capability: z.ZodString;
                    targetId: z.ZodString;
                    providerVersion: z.ZodNumber;
                    capabilityVersion: z.ZodNumber;
                    targetRevision: z.ZodNumber;
                }, z.core.$strict>;
            }, z.core.$strict>>;
        }, z.core.$strict>>>;
    }, z.core.$strict>>;
    state: z.ZodEnum<{
        failed: "failed";
        queued: "queued";
        running: "running";
        completed: "completed";
        canceled: "canceled";
        awaiting_approval: "awaiting_approval";
        awaiting_device: "awaiting_device";
        awaiting_intervention: "awaiting_intervention";
        awaiting_timer: "awaiting_timer";
    }>;
    cancelRequested: z.ZodBoolean;
    outcome: z.ZodEnum<{
        "": "";
        failed: "failed";
        partial: "partial";
        uncertain: "uncertain";
        completed: "completed";
        cancelled: "cancelled";
    }>;
    report: z.ZodNullable<z.ZodObject<{
        state: z.ZodEnum<{
            failed: "failed";
            partial: "partial";
            uncertain: "uncertain";
            completed: "completed";
            cancelled: "cancelled";
        }>;
        steps: z.ZodArray<z.ZodObject<{
            stepId: z.ZodString;
            state: z.ZodEnum<{
                failed: "failed";
                partial: "partial";
                uncertain: "uncertain";
                completed: "completed";
                skipped: "skipped";
                not_run: "not_run";
            }>;
            callId: z.ZodOptional<z.ZodString>;
        }, z.core.$strict>>;
    }, z.core.$strict>>;
    wait: z.ZodOptional<z.ZodObject<{
        waitId: z.ZodString;
        stepId: z.ZodString;
        until: z.ZodISODateTime;
        expiresAt: z.ZodISODateTime;
    }, z.core.$strict>>;
}, z.core.$strict>;
export type MistyRoutineRunRecord = z.output<typeof MistyRoutineRunRecordSchema>;
