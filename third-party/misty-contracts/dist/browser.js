import { z } from "zod";
export const MistyBrowserUrlSchema = z
    .string()
    .min(1)
    .max(8192)
    .refine((value) => {
    if (value === "about:blank")
        return true;
    try {
        const url = new URL(value);
        return (["https:", "http:"].includes(url.protocol) &&
            !url.username &&
            !url.password);
    }
    catch {
        return false;
    }
}, "Browser views support HTTP, HTTPS and about:blank URLs.");
/** Viewport CSS pixels. The host clips to the App view and applies native zoom. */
export const MistyBrowserBoundsSchema = z.strictObject({
    x: z.number().finite().min(0).max(100000),
    y: z.number().finite().min(0).max(100000),
    width: z.number().finite().min(1).max(100000),
    height: z.number().finite().min(1).max(100000),
});
export const MistyBrowserHandleSchema = z.string().uuid();
const handle = z.strictObject({ handle: MistyBrowserHandleSchema });
const empty = z.union([z.undefined(), z.null()]).transform(() => undefined);
export const MistyBrowserInspectionSchema = z.strictObject({
    documentId: z.string().uuid(),
    url: MistyBrowserUrlSchema,
    title: z.string().max(8192),
    text: z.string().max(256 * 1024),
    truncated: z.boolean(),
    interactive: z.array(z.strictObject({
        ref: z.string().min(1).max(128),
        tag: z.string().max(128),
        role: z.string().max(256),
        name: z.string().max(300),
    })).max(500),
    contentTrust: z.literal("untrusted-web-page"),
});
export const MistyBrowserEventSchema = z.discriminatedUnion("type", [
    z.strictObject({
        type: z.literal("page"),
        phase: z.enum(["started", "finished"]),
        url: MistyBrowserUrlSchema,
    }),
    z.strictObject({ type: z.literal("title"), title: z.string().max(512) }),
    z.strictObject({ type: z.literal("favicon"), url: MistyBrowserUrlSchema }),
    z.strictObject({
        type: z.literal("compatibility"),
        kind: z.literal("cloudflare_challenge"),
        url: MistyBrowserUrlSchema,
    }),
    z.strictObject({ type: z.literal("layout") }),
    z.strictObject({
        type: z.literal("state"),
        canBack: z.boolean(), canForward: z.boolean(), loading: z.boolean(), agentAccess: z.boolean(),
        history: z.array(MistyBrowserUrlSchema).max(500),
        error: z.string().max(2000).nullable(), notice: z.string().max(2000).nullable(),
    }),
]);
const contract = (params, result) => ({
    capability: "browser.navigate",
    platforms: ["macos"],
    params,
    result,
});
export const mistyBrowserContracts = {
    "browser.create": contract(z.strictObject({
        url: MistyBrowserUrlSchema.optional(),
        bounds: MistyBrowserBoundsSchema,
        nativeLiveResize: z.boolean().default(false),
    }), z.strictObject({
        handle: MistyBrowserHandleSchema,
        contextId: z.string().uuid(),
        url: MistyBrowserUrlSchema,
    })),
    "browser.layout": contract(handle.extend({
        bounds: MistyBrowserBoundsSchema,
        visible: z.boolean(),
        nativeLiveResize: z.boolean().default(false),
    }), empty),
    "browser.navigate": contract(handle.extend({ url: MistyBrowserUrlSchema }), empty),
    "browser.back": contract(handle, empty),
    "browser.forward": contract(handle, empty),
    "browser.reload": contract(handle, empty),
    "browser.close": contract(handle, empty),
    "browser.inspect": {
        ...contract(handle, MistyBrowserInspectionSchema),
        capability: "browser.inspect",
    },
    "browser.click": {
        ...contract(handle.extend({
            documentId: z.string().uuid(),
            elementRef: z.string().min(1).max(128),
        }), empty),
        capability: "browser.interact",
    },
    "browser.overlay": contract(handle.extend({
        reason: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
        active: z.boolean(),
    }), empty),
};
export function isMistyBrowserMethod(method) {
    return Object.hasOwn(mistyBrowserContracts, method);
}
//# sourceMappingURL=browser.js.map