import { z } from "zod";
export declare const MistyAiControlsSnapshotSchema: z.ZodObject<{
    available: z.ZodBoolean;
    following: z.ZodBoolean;
    proposal: z.ZodOptional<z.ZodObject<{
        id: z.ZodString;
        kind: z.ZodString;
        state: z.ZodEnum<{
            proposed: "proposed";
            applying: "applying";
            applied: "applied";
            rejected: "rejected";
            stale: "stale";
            failed: "failed";
        }>;
        stale: z.ZodBoolean;
        replacement: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
}, z.core.$strict>;
/** Operate the registered, active App surface; never expose account AI sessions or transcripts. */
export declare const mistyAiControlsContracts: {
    readonly "ai.snapshot": {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodObject<{
            available: z.ZodBoolean;
            following: z.ZodBoolean;
            proposal: z.ZodOptional<z.ZodObject<{
                id: z.ZodString;
                kind: z.ZodString;
                state: z.ZodEnum<{
                    proposed: "proposed";
                    applying: "applying";
                    applied: "applied";
                    rejected: "rejected";
                    stale: "stale";
                    failed: "failed";
                }>;
                stale: z.ZodBoolean;
                replacement: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
    };
    readonly "ai.action.run": {
        readonly params: z.ZodObject<{
            actionId: z.ZodString;
            selectionHash: z.ZodOptional<z.ZodString>;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "ai.proposal.decide": {
        readonly params: z.ZodObject<{
            proposalId: z.ZodString;
            decision: z.ZodEnum<{
                accept: "accept";
                reject: "reject";
                refine: "refine";
            }>;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyAiControlsSnapshot = z.output<typeof MistyAiControlsSnapshotSchema>;
export type MistyAiControlsMethod = keyof typeof mistyAiControlsContracts;
export type MistyAiControlsParams<M extends MistyAiControlsMethod> = z.input<(typeof mistyAiControlsContracts)[M]["params"]>;
export type MistyAiControlsResult<M extends MistyAiControlsMethod> = z.output<(typeof mistyAiControlsContracts)[M]["result"]>;
export declare function isMistyAiControlsMethod(method: string): method is MistyAiControlsMethod;
