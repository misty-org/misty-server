import { z } from "zod";
/** A resolved container, never a freeform provider name or a request to guess. */
export declare const MistyTaskDestinationSchema: z.ZodObject<{
    targetId: z.ZodUUID;
    containerReference: z.ZodString;
    label: z.ZodString;
}, z.core.$strict>;
export declare const MistyTaskCreateInputSchema: z.ZodObject<{
    title: z.ZodString;
    text: z.ZodString;
    destination: z.ZodObject<{
        targetId: z.ZodUUID;
        containerReference: z.ZodString;
        label: z.ZodString;
    }, z.core.$strict>;
    dueDate: z.ZodOptional<z.ZodISODate>;
    source: z.ZodObject<{
        reference: z.ZodString;
        label: z.ZodString;
    }, z.core.$strict>;
}, z.core.$strict>;
export declare const MistyTaskCreateResultSchema: z.ZodObject<{
    taskReference: z.ZodString;
    destination: z.ZodObject<{
        targetId: z.ZodUUID;
        containerReference: z.ZodString;
        label: z.ZodString;
    }, z.core.$strict>;
    title: z.ZodString;
    text: z.ZodString;
    dueDate: z.ZodOptional<z.ZodISODate>;
    source: z.ZodObject<{
        reference: z.ZodString;
        label: z.ZodString;
    }, z.core.$strict>;
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
export declare const mistyTaskCapabilities: {
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
