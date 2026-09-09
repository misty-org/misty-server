import { z } from "zod";
export const MISTY_CAPABILITY_PROTOCOL_VERSION = 1;
export const MistyCapabilityNameSchema = z.string().max(160).regex(/^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$/);
export const MistyProviderIdSchema = z.string().max(240).regex(/^[a-z][a-z0-9.-]+\/[a-z][a-z0-9_-]*$/);
const id = z.string().uuid();
/** Existing trusted-device records use device_<uuid>; older clients use UUIDs. */
export const MistyCapabilityDeviceIdSchema = z.string().refine((value) => id.safeParse(value.startsWith("device_") ? value.slice(7) : value).success, "A trusted device identity is required.");
const version = z.number().int().positive().max(2147483647);
const scope = z.string().max(160).regex(/^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$/);
const timestamp = z.iso.datetime({ offset: true });
const digest = z.string().regex(/^[a-f0-9]{64}$/);
const unique = (items) => new Set(items).size === items.length;
export const MistyCapabilityValueSchema = z.json().superRefine((value, ctx) => {
    const serialized = JSON.stringify(value);
    if (new TextEncoder().encode(serialized).byteLength > 512 * 1024)
        ctx.addIssue({ code: "custom", message: "Capability values must not exceed 512 KiB." });
});
/** Schemas are data. Remote references and executable extensions are forbidden. */
export const MistyCapabilityJsonSchema = z.preprocess((value, ctx) => {
    const safe = (item, depth) => {
        if (depth > 32)
            return false;
        if (!item || typeof item !== "object")
            return true;
        return Object.entries(item).every(([key, child]) => !["__proto__", "constructor", "prototype"].includes(key) && safe(child, depth + 1));
    };
    if (!safe(value, 0)) {
        ctx.addIssue({ code: "custom", message: "Unsafe or excessively nested schema." });
        return z.NEVER;
    }
    return value;
}, z.record(z.string(), z.json()).superRefine((schema, ctx) => {
    const visit = (value, depth) => {
        if (depth > 32)
            return false;
        if (!value || typeof value !== "object")
            return true;
        return Object.entries(value).every(([key, child]) => {
            if (["__proto__", "constructor", "prototype", "$dynamicRef"].includes(key))
                return false;
            if (key === "$ref" && (typeof child !== "string" || !child.startsWith("#/$defs/")))
                return false;
            return visit(child, depth + 1);
        });
    };
    if (new TextEncoder().encode(JSON.stringify(schema)).byteLength > 64 * 1024 || !visit(schema, 0))
        ctx.addIssue({ code: "custom", message: "Schema exceeds limits or contains unsafe references." });
}));
export const MistyCapabilityEffectsSchema = z.strictObject({
    kind: z.enum(["read", "write", "send", "execute", "destructive"]),
    incidental: z.array(z.string().min(1).max(300)).max(16).default([]),
    approval: z.enum(["none", "scoped", "interactive"]),
    retry: z.enum(["read_only", "idempotent", "reconcile", "never"]),
});
export const MistyCapabilityDefinitionSchema = z.strictObject({
    name: MistyCapabilityNameSchema,
    version,
    description: z.string().min(1).max(2000),
    inputSchema: MistyCapabilityJsonSchema,
    outputSchema: MistyCapabilityJsonSchema,
    requiredScopes: z.array(scope).min(1).max(32).refine(unique, "Duplicate scopes."),
    effects: MistyCapabilityEffectsSchema,
}).superRefine((value, ctx) => {
    if (value.effects.kind !== "read" && value.effects.approval === "none")
        ctx.addIssue({ code: "custom", path: ["effects", "approval"], message: "Mutations require scoped or interactive approval." });
    if (value.effects.kind !== "read" && value.effects.retry === "read_only")
        ctx.addIssue({ code: "custom", path: ["effects", "retry"], message: "Only reads may use read-only retries." });
});
export const MistyCapabilityOriginSchema = z.string().max(2048).refine((value) => {
    try {
        const url = new URL(value);
        return url.protocol === "https:" && url.origin === value && !url.username && !url.password;
    }
    catch {
        return false;
    }
}, "An exact HTTPS origin is required.");
export const MistyProviderRouteSchema = z.discriminatedUnion("kind", [
    z.strictObject({ kind: z.literal("server"), adapter: z.string().regex(/^[a-z][a-z0-9_-]{0,79}$/) }),
    z.strictObject({ kind: z.literal("native"), adapter: z.string().regex(/^[a-z][a-z0-9_-]{0,79}$/) }),
    z.strictObject({ kind: z.literal("browser"), adapter: z.string().regex(/^[a-z][a-z0-9_-]{0,79}$/).optional(), adapterVersion: version.optional(), origins: z.array(MistyCapabilityOriginSchema).min(1).max(32).refine(unique), hints: z.array(z.string().max(1000)).max(20).default([]) }),
    // The endpoint and credentials are configured by the verified installation, not invocation input.
    z.strictObject({ kind: z.literal("backend"), connectionId: id }),
    z.strictObject({ kind: z.literal("view"), instanceId: id }),
]).superRefine((route, ctx) => {
    if (route.kind === "browser" && ((route.adapter === undefined) !== (route.adapterVersion === undefined)))
        ctx.addIssue({ code: "custom", message: "Declare the browser adapter and its version together." });
});
export const MistyCapabilityProviderSchema = z.strictObject({
    id: MistyProviderIdSchema,
    version,
    label: z.string().min(1).max(200),
    route: MistyProviderRouteSchema,
    capabilities: z.array(MistyCapabilityDefinitionSchema).min(1).max(100).refine((items) => unique(items.map((item) => item.name)), "Duplicate capability names."),
});
export const MistyCapabilityManifestSchema = z.strictObject({
    protocol: z.literal(MISTY_CAPABILITY_PROTOCOL_VERSION),
    providers: z.array(MistyCapabilityProviderSchema).max(32).refine((items) => unique(items.map((item) => item.id))),
});
/** An intended account/profile binding; it is not proof of the signed-in identity. */
export const MistyBrowserCapabilityBindingSchema = z.strictObject({
    kind: z.literal("browser"), deviceId: MistyCapabilityDeviceIdSchema, profileId: digest, accountBindingId: id,
    origins: z.array(MistyCapabilityOriginSchema).min(1).max(32).refine(unique), contextId: id.optional(),
    scopeId: z.string().min(1).max(256).optional(), accountIdentity: z.string().min(1).max(320).optional(),
});
/** Trusted Misty controls only. This is deliberately not an app RPC method:
 * installing or registering a provider never grants targets or cross-app access. */
export const MistyCapabilityTargetConfigurationSchema = z.strictObject({
    targetId: id,
    expectedRevision: z.number().int().min(0).max(2147483646),
    providerId: MistyProviderIdSchema,
    providerVersion: version,
    spaceId: z.string().regex(/^[A-Za-z0-9_-]{1,256}$/).optional(),
    label: z.string().min(1).max(200),
    capabilities: z.array(MistyCapabilityNameSchema).min(1).max(100).refine(unique),
    callerApps: z.array(z.string().regex(/^[a-z][a-z0-9.-]{1,79}$/)).max(100).refine(unique),
    browser: MistyBrowserCapabilityBindingSchema.optional(),
});
/** These identities are issued/verified by Misty; declaring one does not grant access. */
export const MistyCapabilityTargetSchema = z.strictObject({
    id,
    revision: version,
    appId: z.string().min(1).max(200),
    providerId: MistyProviderIdSchema,
    providerVersion: version,
    // Existing Space identities are opaque (for example, space_<uuid>).
    spaceId: z.string().regex(/^[A-Za-z0-9_-]{1,256}$/).optional(),
    label: z.string().min(1).max(200),
    binding: z.discriminatedUnion("kind", [
        z.strictObject({ kind: z.literal("resource"), resourceId: z.string().min(1).max(300) }),
        MistyBrowserCapabilityBindingSchema,
        z.strictObject({ kind: z.literal("native"), deviceId: MistyCapabilityDeviceIdSchema, resourceHandle: id }),
        z.strictObject({ kind: z.literal("backend"), connectionId: id }),
        z.strictObject({ kind: z.literal("view"), deviceId: MistyCapabilityDeviceIdSchema, instanceId: id }),
    ]),
});
/** Trusted control inventory, including disabled targets; existence is not availability. */
export const MistyCapabilityTargetPageSchema = z.strictObject({
    targets: z.array(z.strictObject({
        target: MistyCapabilityTargetSchema,
        capabilities: z.array(MistyCapabilityNameSchema).max(100).refine(unique),
        callerApps: z.array(z.string().regex(/^[a-z][a-z0-9.-]{1,79}$/)).max(100).refine(unique),
        enabled: z.boolean(),
    })).max(100),
    nextCursor: id.nullable(),
});
export const MistyCapabilityAvailabilitySchema = z.strictObject({
    state: z.enum(["available", "device_required", "authentication_required", "account_confirmation_required", "view_closed", "unavailable", "revoked"]),
    observedAt: timestamp,
    reason: z.string().max(1000).optional(),
});
export const MistyCapabilityEvidenceSchema = z.strictObject({
    targetId: id,
    observedAt: timestamp,
    kind: z.enum(["resource", "browser", "command", "provider"]),
    reference: z.string().min(1).max(2048),
    revision: z.string().max(200).optional(),
    excerpt: z.string().max(8000).optional(),
});
const wait = {
    waitId: id,
    expiresAt: timestamp,
    reason: z.string().min(1).max(1000),
};
export const MistyCapabilityOutcomeSchema = z.discriminatedUnion("status", [
    z.strictObject({ status: z.literal("success"), result: MistyCapabilityValueSchema, evidence: z.array(MistyCapabilityEvidenceSchema).max(100), partial: z.boolean() }),
    z.strictObject({ status: z.literal("failure"), code: z.string().min(1).max(100), message: z.string().min(1).max(2000), retryable: z.boolean() }),
    z.strictObject({ status: z.literal("approval_required"), ...wait, approvalId: id }),
    z.strictObject({ status: z.literal("device_required"), ...wait, deviceId: MistyCapabilityDeviceIdSchema }),
    z.strictObject({ status: z.literal("user_intervention_required"), ...wait, action: z.enum(["sign_in", "account_confirmation", "challenge", "open_target", "review"]) }),
    z.strictObject({ status: z.literal("uncertain"), effectId: id, reason: z.string().min(1).max(2000), evidence: z.array(MistyCapabilityEvidenceSchema).max(100) }),
]);
export const MistyCapabilityInvocationSchema = z.strictObject({
    requestId: id,
    capability: MistyCapabilityNameSchema,
    capabilityVersion: version,
    providerId: MistyProviderIdSchema,
    providerVersion: version,
    targetId: id,
    targetRevision: version,
    input: MistyCapabilityValueSchema,
    deadline: timestamp,
});
/** Host-issued execution envelope. App callers cannot select a run or grant identity. */
export const MistyCapabilityExecutionSchema = MistyCapabilityInvocationSchema.extend({
    runId: id,
    effectId: id,
    grantIds: z.array(id).max(100),
});
const providerPath = z.strictObject({ providerID: MistyProviderIdSchema });
const requestPath = z.strictObject({ requestID: id });
export const mistyCapabilityServerContracts = {
    "capabilities.providers.register": {
        verb: "POST", path: "/capabilities/providers",
        params: z.strictObject({ body: z.strictObject({ manifestDigest: digest, provider: MistyCapabilityProviderSchema }) }),
        result: z.strictObject({ provider: MistyCapabilityProviderSchema, availability: MistyCapabilityAvailabilitySchema }),
    },
    "capabilities.providers.unregister": {
        verb: "DELETE", path: "/capabilities/providers/{providerID}",
        params: z.strictObject({ path: providerPath }), result: z.undefined(),
    },
    "capabilities.providers.availability": {
        verb: "PUT", path: "/capabilities/providers/{providerID}/availability",
        params: z.strictObject({ path: providerPath, body: MistyCapabilityAvailabilitySchema }), result: z.undefined(),
    },
    "capabilities.discover": {
        verb: "POST", path: "/capabilities/discover",
        params: z.strictObject({ body: z.strictObject({ capability: MistyCapabilityNameSchema.optional(), targetId: id.optional(), cursor: z.string().max(2048).optional(), limit: z.number().int().min(1).max(100).default(50) }) }),
        result: z.strictObject({ providers: z.array(MistyCapabilityProviderSchema).max(100), nextCursor: z.string().max(2048).nullable() }),
    },
    "capabilities.targets.resolve": {
        verb: "POST", path: "/capabilities/targets/resolve",
        params: z.strictObject({ body: z.strictObject({ capability: MistyCapabilityNameSchema, targetId: id.optional(), contextId: id.optional(), spaceId: z.string().regex(/^[A-Za-z0-9_-]{1,256}$/).optional(), providerId: MistyProviderIdSchema.optional() }) }),
        result: z.strictObject({ targets: z.array(MistyCapabilityTargetSchema).max(100) }),
    },
    "capabilities.invoke": {
        verb: "POST", path: "/capabilities/invocations",
        params: z.strictObject({ body: MistyCapabilityInvocationSchema }),
        result: z.strictObject({ requestId: id, runId: id }),
    },
    "capabilities.result": {
        verb: "GET", path: "/capabilities/invocations/{requestID}",
        params: z.strictObject({ path: requestPath }),
        result: z.strictObject({ requestId: id, state: z.enum(["queued", "running", "waiting", "completed", "failed", "cancelled", "uncertain"]), outcome: MistyCapabilityOutcomeSchema.nullable() }),
    },
    "capabilities.cancel": {
        verb: "POST", path: "/capabilities/invocations/{requestID}/cancel",
        params: z.strictObject({ path: requestPath }), result: z.undefined(),
    },
};
//# sourceMappingURL=capabilities.js.map