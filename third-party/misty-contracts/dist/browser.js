import { z } from "zod";
import { mistyBrowserProviders } from "./browser-providers.js";
export * from "./browser-providers.js";
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
/** A provider account is an app-local identity, never a cookie or an API token. */
export const MistyBrowserProviderSchema = z.strictObject({
    id: z.enum(Object.keys(mistyBrowserProviders)),
    accountId: z.string().min(1).max(160).regex(/^[a-zA-Z0-9_-]+$/),
});
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
/** No arbitrary JavaScript, selectors, credentials, or filesystem paths cross this boundary. */
export const MistyBrowserInteractionSchema = z.discriminatedUnion("kind", [
    z.strictObject({ kind: z.literal("fill"), elementRef: z.string().min(1).max(128), text: z.string().max(64 * 1024) }),
    z.strictObject({ kind: z.literal("select"), elementRef: z.string().min(1).max(128), values: z.array(z.string().max(1000)).min(1).max(100) }),
    z.strictObject({ kind: z.literal("scroll"), elementRef: z.string().min(1).max(128).optional(), x: z.number().int().min(-4000).max(4000), y: z.number().int().min(-4000).max(4000) }),
    z.strictObject({ kind: z.literal("key"), elementRef: z.string().min(1).max(128), key: z.enum(["Enter", "Escape", "Tab", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End"]) }),
]);
/** Native observations identify a local profile, never authenticate an account.
 * Known login pages report required; every other page remains unknown. */
export const MistyBrowserTargetObservationSchema = z.strictObject({
    scopeId: z.string().min(1).max(256),
    profileId: z.string().regex(/^[0-9a-f]{64}$/).optional(),
    providerId: z.string().min(1).max(100).optional(),
    origin: z.string().min(1).max(8192),
    authentication: z.enum(["required", "unknown"]),
    accountIdentity: z.literal("unverified"),
    trust: z.literal("host-observation"),
    observedAt: z.string().datetime({ offset: true }),
});
export const MistyBrowserInspectionSchema = z.strictObject({
    /** Provider-extracted observations are untrusted facts, never permissions. */
    semantic: z.record(z.string(), z.json()).nullable().optional(),
    target: MistyBrowserTargetObservationSchema.optional(),
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
    "browser.availability": contract(z.strictObject({}), z.strictObject({
        available: z.boolean(),
        reason: z.string().max(1000).optional(),
        persistent: z.boolean(),
        supportedProviders: z.array(MistyBrowserProviderSchema.shape.id).optional(),
        profileCleanup: z.boolean().optional(),
    })),
    "browser.removeAccount": contract(z.strictObject({ provider: MistyBrowserProviderSchema }), empty),
    "browser.create": contract(z.strictObject({
        url: MistyBrowserUrlSchema.optional(),
        provider: MistyBrowserProviderSchema.optional(),
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
    "browser.setZoom": contract(handle.extend({ factor: z.number().min(0.25).max(5) }), empty),
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
    "browser.interact": {
        ...contract(handle.extend({ documentId: z.string().uuid(), action: MistyBrowserInteractionSchema }), z.strictObject({ attempted: z.literal(true) })),
        capability: "browser.interact",
    },
    "browser.type": {
        ...contract(handle.extend({
            documentId: z.string().uuid(),
            elementRef: z.string().min(1).max(128),
            text: z.string().max(20000),
        }), z.strictObject({ prepared: z.literal(true) })),
        capability: "browser.interact",
    },
    // Read-only, same-origin requests. No caller-supplied headers, credentials, or scripts.
    "browser.request": {
        ...contract(handle.extend({ path: z.string().min(1).max(2048).regex(/^\/(?!\/)/) }), z.strictObject({ status: z.number().int(), body: z.string().max(262144), truncated: z.boolean() })),
        capability: "browser.inspect",
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