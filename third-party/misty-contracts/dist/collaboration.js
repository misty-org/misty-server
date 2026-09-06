import { z } from "zod";
export const MISTY_COLLABORATION_SEND_BYTES = 512 * 1024;
export const MISTY_COLLABORATION_RECEIVE_BYTES = 8 * 1024 * 1024 + 1024;
const base64 = (bytes) => z.string().max(Math.ceil(bytes / 3) * 4)
    .regex(/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/);
export const MistyCollaborationRoleSchema = z.enum(["creator", "editor", "viewer"]);
export const MistyCollaborationResourceSchema = z.enum(["note", "drawing"]);
const handle = z.strictObject({ handle: z.string().uuid() });
const empty = z.union([z.null(), z.undefined()]).transform(() => undefined);
export const MistyCollaborationEventSchema = z.discriminatedUnion("type", [
    z.strictObject({ type: z.literal("open") }),
    z.strictObject({ type: z.literal("binary"), data: base64(MISTY_COLLABORATION_RECEIVE_BYTES) }),
    z.strictObject({ type: z.literal("text"), data: z.string().max(MISTY_COLLABORATION_SEND_BYTES) }),
    z.strictObject({ type: z.literal("error"), message: z.string().max(512) }),
    z.strictObject({ type: z.literal("close"), code: z.number().int().min(1000).max(4999), reason: z.string().max(240) }),
]);
/** The host keeps join tickets, server URLs, sockets and their lifetime. */
export const mistyCollaborationContracts = {
    "collaboration.open": {
        params: z.strictObject({ resource: MistyCollaborationResourceSchema, resourceId: z.string().regex(/^[a-zA-Z0-9_-]{1,128}$/) }),
        result: handle.extend({ role: MistyCollaborationRoleSchema }),
    },
    "collaboration.send": { params: handle.extend({ data: base64(MISTY_COLLABORATION_SEND_BYTES) }), result: empty },
    "collaboration.close": { params: handle, result: empty },
};
export function isMistyCollaborationMethod(method) {
    return Object.hasOwn(mistyCollaborationContracts, method);
}
//# sourceMappingURL=collaboration.js.map