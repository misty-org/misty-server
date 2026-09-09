import { z } from "zod";
export const MistyEditorPreferencesSchema = z.strictObject({
    autosaveDelayMs: z.number().min(0).max(30000),
    fontFamily: z.string().max(512), fontSize: z.number().min(8).max(32),
    formatOnSave: z.boolean(), interfaceScale: z.number().min(0.8).max(1.5),
    lineNumbers: z.boolean(), tabSize: z.number().min(1).max(8),
    theme: z.string().max(160), wordWrap: z.boolean(),
});
const empty = z.strictObject({});
const done = z.union([z.null(), z.undefined()]).transform(() => undefined);
export const mistyCodeControlsContracts = {
    "code.preferences.update": {
        params: z.discriminatedUnion("key", [
            z.strictObject({ key: z.literal("font_size"), value: z.number().min(8).max(32) }),
            z.strictObject({ key: z.literal("interface_scale"), value: z.number().min(0.8).max(1.5) }),
        ]), result: done,
    },
    "code.models.open": { params: empty, result: done },
    "code.terminal.toggle": {
        params: z.strictObject({ placement: z.enum(["down", "up", "left", "right", "current"]).default("down") }), result: done,
    },
    "code.rewrite": {
        params: z.strictObject({
            requestId: z.string().uuid(), instruction: z.string().min(1).max(8192),
            selection: z.string().max(128 * 1024), language: z.string().max(80), filename: z.string().max(512),
        }), result: z.string().max(512 * 1024),
    },
    "code.rewrite.cancel": { params: z.strictObject({ requestId: z.string().uuid() }), result: done },
};
export function isMistyCodeControlsMethod(method) {
    return Object.hasOwn(mistyCodeControlsContracts, method);
}
//# sourceMappingURL=code-controls.js.map