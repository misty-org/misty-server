import { z } from "zod";
export const MistyAiControlsSnapshotSchema = z.strictObject({
    available: z.boolean(),
    following: z.boolean(),
    proposal: z.strictObject({
        id: z.string().min(1).max(256),
        kind: z.string().min(1).max(80),
        state: z.enum(["proposed", "applying", "applied", "rejected", "stale", "failed"]),
        stale: z.boolean(),
        replacement: z.string().max(65536).optional(),
    }).optional(),
});
const empty = z.union([z.null(), z.undefined()]).transform(() => undefined);
/** Operate the registered, active App surface; never expose account AI sessions or transcripts. */
export const mistyAiControlsContracts = {
    "ai.snapshot": { params: z.strictObject({}), result: MistyAiControlsSnapshotSchema },
    "ai.action.run": {
        params: z.strictObject({ actionId: z.string().min(1).max(256), selectionHash: z.string().min(1).max(256).optional() }), result: empty,
    },
    "ai.proposal.decide": {
        params: z.strictObject({ proposalId: z.string().min(1).max(256), decision: z.enum(["accept", "reject", "refine"]) }), result: empty,
    },
};
export function isMistyAiControlsMethod(method) {
    return Object.hasOwn(mistyAiControlsContracts, method);
}
//# sourceMappingURL=ai-controls.js.map