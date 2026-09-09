import { z } from "zod";
import { mistyBrowserProviders } from "./browser-providers.js";
export const MistyProviderResourceKindSchema = z.enum([
    "file",
    "document",
    "task",
    "event",
    "conversation",
]);
export const MistyProviderDestinationSchema = z.enum([
    "library",
    "journal",
    "planner",
    "chat",
]);
export const MistyProviderSyncStateSchema = z.enum([
    "snapshot",
    "pending",
    "syncing",
    "live",
    "periodic",
    "delayed",
    "partial",
    "paused",
    "device_required",
    "authentication_required",
    "source_deleted",
    "access_lost",
    "left_container",
    "stopped",
]);
const reference = z.string().min(1).max(2048);
const timestamp = z.iso.datetime({ offset: true });
export const MistyProviderResourceSchema = z.strictObject({
    id: reference,
    kind: MistyProviderResourceKindSchema,
    title: z.string().min(1).max(1000),
    url: z
        .url()
        .max(2048)
        .refine((value) => {
        const u = new URL(value);
        const secret = /^(code|state|token|access_token|id_token|refresh_token|session_state|samlresponse|relaystate|ticket)$/i;
        return (u.protocol === "https:" &&
            !u.username &&
            !u.password &&
            (!u.port || u.port === "443") &&
            ![
                ...u.searchParams.keys(),
                ...new URLSearchParams(u.hash.slice(1)).keys(),
            ].some((key) => secret.test(key)));
    }),
    revision: z.string().max(256),
    observedAt: timestamp,
    text: z
        .string()
        .max(256000)
        .refine((value) => new TextEncoder().encode(value).byteLength <= 256000, "The shared text exceeds the capture limit.")
        .default(""),
    partial: z.boolean(),
    limitations: z.array(z.string().max(500)).max(20).default([]),
    // Account credentials, DOM control references and authorization URLs are never resource data.
});
export const MistyProviderSourceSchema = z
    .strictObject({
    provider: z.enum(Object.keys(mistyBrowserProviders)),
    accountId: reference,
    resourceId: reference,
    selection: z.enum(["item", "container"]),
    includeFutureItems: z.boolean().default(false),
})
    .refine((value) => value.selection !== "container" || value.includeFutureItems, "Container sharing must explicitly include future items.");
/** Shared projections deliberately exclude source account IDs, device IDs and sync cursors. */
export const MistySharedSourceSchema = z.strictObject({
    id: reference,
    spaceId: reference,
    destination: MistyProviderDestinationSchema,
    provider: z.string().min(1).max(100),
    contributorId: reference,
    contributorName: z.string().max(1000).default("Space member"),
    resource: MistyProviderResourceSchema,
    state: MistyProviderSyncStateSchema,
    syncMode: z.enum(["none", "push", "poll", "device"]).default("none"),
    version: z.number().int().positive(),
    updatedAt: timestamp,
    lastSuccessAt: timestamp.nullable(),
    canManage: z.boolean(),
});
export const MistyProviderFeatureSchema = z.enum([
    "read",
    "search",
    "export",
    "sync",
    "actions",
]);
export const MistyProviderAvailabilitySchema = z.strictObject({
    provider: z.string(),
    features: z.array(MistyProviderFeatureSchema),
    syncMode: z.enum(["none", "push", "poll", "device"]),
    reason: z.string(),
});
const space = { spaceID: reference };
const source = { ...space, sourceID: reference };
export const mistyProviderResourceContracts = {
    "sources.availability": {
        verb: "GET",
        path: "/provider-sources/availability",
        params: z.strictObject({}),
        result: z.strictObject({
            providers: z.array(MistyProviderAvailabilitySchema),
        }),
    },
    "sources.list": {
        verb: "GET",
        path: "/spaces/{spaceID}/sources",
        params: z.strictObject({ path: z.strictObject(space) }),
        result: z.strictObject({ sources: z.array(MistySharedSourceSchema) }),
    },
    "sources.preview": {
        verb: "POST",
        path: "/spaces/{spaceID}/sources/preview",
        params: z.strictObject({
            path: z.strictObject(space),
            body: z.strictObject({
                source: MistyProviderSourceSchema,
                destination: MistyProviderDestinationSchema,
                resource: MistyProviderResourceSchema,
                updates: z.boolean(),
            }),
        }),
        result: z.strictObject({
            token: reference,
            expiresAt: timestamp,
            audience: z.string(),
            resource: MistyProviderResourceSchema,
        }),
    },
    "sources.create": {
        verb: "POST",
        path: "/spaces/{spaceID}/sources",
        params: z.strictObject({
            path: z.strictObject(space),
            body: z.strictObject({ token: reference }),
        }),
        result: MistySharedSourceSchema,
    },
    "sources.copy": {
        verb: "POST",
        path: "/spaces/{spaceID}/sources/{sourceID}/copy",
        params: z.strictObject({
            path: z.strictObject(source),
            body: z.strictObject({
                version: z.number().int().positive(),
                requestId: z.uuid(),
            }),
        }),
        result: z.strictObject({
            route: z.string().startsWith("/"),
            id: reference,
        }),
    },
    "sources.control": {
        verb: "POST",
        path: "/spaces/{spaceID}/sources/{sourceID}/control",
        params: z.strictObject({
            path: z.strictObject(source),
            body: z.strictObject({
                action: z.enum(["refresh", "pause", "resume", "stop"]),
                version: z.number().int().positive(),
            }),
        }),
        result: MistySharedSourceSchema,
    },
};
export const mistyProviderDestinations = {
    file: "library",
    document: "journal",
    task: "planner",
    event: "planner",
    conversation: "chat",
};
/** Availability is evidence, never inferred from a provider's presence in the catalog. */
export function providerSupports(availability, feature) {
    return availability?.features.includes(feature) ?? false;
}
//# sourceMappingURL=provider-resources.js.map