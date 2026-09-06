import { z } from "zod";
// Validate iteratively before recursive parsing/stringification: component transports
// can pass cyclic objects even though an HTTP JSON body cannot contain them.
export const MistyViewStateSchema = z.custom((root) => {
    const pending = [[root, 0]], seen = new Set();
    let nodes = 0, bytes = 0;
    while (pending.length) {
        const [value, depth, leaving] = pending.pop();
        if (leaving) {
            seen.delete(value);
            continue;
        }
        if (++nodes > 8192 || depth > 24)
            return false;
        if (value === null || typeof value === "boolean")
            bytes += 5;
        else if (typeof value === "number") {
            if (!Number.isFinite(value))
                return false;
            bytes += 24;
        }
        else if (typeof value === "string") {
            if (value.length > 65536)
                return false;
            bytes += new TextEncoder().encode(JSON.stringify(value)).length;
        }
        else if (typeof value === "object") {
            if (seen.has(value))
                return false;
            seen.add(value);
            pending.push([value, depth, true]);
            if (!Array.isArray(value) && Object.getPrototypeOf(value) !== Object.prototype && Object.getPrototypeOf(value) !== null)
                return false;
            const keys = Object.keys(value);
            if (keys.length > 8192)
                return false;
            if (Array.isArray(value) && keys.length !== value.length)
                return false;
            for (const key of keys) {
                if (key.length > 65536)
                    return false;
                const descriptor = Object.getOwnPropertyDescriptor(value, key);
                if (!descriptor || !("value" in descriptor))
                    return false;
                bytes += new TextEncoder().encode(JSON.stringify(key)).length + 2;
                pending.push([descriptor.value, depth + 1]);
            }
            bytes += 2;
        }
        else
            return false;
        if (bytes > 65536)
            return false;
    }
    return true;
}, "View state must be bounded JSON (64 KiB, depth 24).");
const id = z.string().min(1).max(256);
export const MistyViewTitleSchema = z.string().min(1).max(160).refine(value => !/[\u0000-\u001f\u007f]/.test(value));
export const MistyViewPlacementSchema = z.enum(["tab", "left", "right", "up", "down"]);
export const MistyWorkspaceViewSchema = z.strictObject({
    viewId: id, panelId: id, title: MistyViewTitleSchema, route: z.string().max(2048),
    state: MistyViewStateSchema, sidebarVisible: z.boolean(), active: z.boolean(), focused: z.boolean(),
});
export const MistyWorkspaceSnapshotSchema = z.strictObject({ revision: z.number().int().nonnegative().safe(), views: z.array(MistyWorkspaceViewSchema).max(128) });
export const MistyWorkspaceUpdateSchema = z.strictObject({
    viewId: id, state: MistyViewStateSchema.optional(), title: MistyViewTitleSchema.optional(), sidebarVisible: z.boolean().optional(),
}).refine(value => value.state !== undefined || value.title !== undefined || value.sidebarVisible !== undefined, "Specify a view change.");
export const MistyWorkspacePlaceSchema = z.strictObject({ viewId: id, targetViewId: id, placement: MistyViewPlacementSchema });
const voidResult = z.union([z.null(), z.undefined()]).transform(() => undefined);
export const mistyWorkspaceContracts = {
    "workspace.snapshot": { params: z.strictObject({}), result: MistyWorkspaceSnapshotSchema },
    "workspace.update": { params: MistyWorkspaceUpdateSchema, result: voidResult },
    "workspace.focus": { params: z.strictObject({ viewId: id }), result: voidResult },
    "workspace.close": { params: z.strictObject({ viewId: id }), result: voidResult },
    "workspace.place": { params: MistyWorkspacePlaceSchema, result: voidResult },
};
//# sourceMappingURL=workspace.js.map