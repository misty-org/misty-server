import { z } from "zod";
import { EmptyParamsSchema, IdentifierSchema, SpacePathSchema, } from "./requests.js";
// Public metadata only. Provider credentials and OAuth verifier state stay on the server.
export const ConnectedAccountSchema = z.object({
    id: z.string(),
    provider: z.string(),
    account_id: z.string(),
    account_display: z.string(),
    capabilities: z.array(z.string()).nullable(),
    granted_scopes: z.array(z.string()).nullable(),
    status: z.enum(["active", "needs_attention", "revoked"]),
    last_error_code: z.string().optional(),
    expires_at: z.string().nullable().optional(),
});
export const SpaceIntegrationSchema = z.object({
    id: z.string(),
    space_id: z.string(),
    provider: z.string(),
    display_name: z.string(),
    granted_permissions: z.array(z.string()).nullable(),
    status: z.string(),
    connected_by_user_id: z.string(),
    created_at: z.string(),
    updated_at: z.string(),
});
const provider = z.string().regex(/^[a-z][a-z0-9_-]{0,79}$/);
const capability = z.string().regex(/^[a-z][a-z0-9_]{0,79}$/);
const returnPath = z
    .string()
    .min(1)
    .max(2048)
    .refine((value) => value.startsWith("/") &&
    !value.startsWith("//") &&
    !/[\\\u0000-\u0020\u007f]/.test(value));
export const mistyConnectionContracts = {
    "connections.list": {
        verb: "GET",
        path: "/connections",
        params: EmptyParamsSchema,
        result: z.object({
            connections: z.array(ConnectedAccountSchema).nullable(),
            providers: z.record(z.string(), z.boolean()).optional(),
        }),
    },
    "connections.remove": {
        verb: "DELETE",
        path: "/connections/{connectionID}",
        params: z.strictObject({ path: SpacePathSchema.extend({ connectionID: IdentifierSchema }) }),
        result: z.undefined(),
    },
    "connections.authorize": {
        verb: "POST",
        path: "/connections/{provider}/authorize",
        params: z.strictObject({
            path: SpacePathSchema.extend({ provider }),
            body: z.strictObject({
                capabilities: z.array(capability).min(1).max(40),
                return_to: returnPath,
            }),
        }),
        result: z.object({
            provider: z.string(),
            authorization_url: z.url(),
            state_expires_at: z.string().optional(),
        }),
    },
    "integrations.list": {
        verb: "GET",
        path: "/spaces/{spaceID}/integrations",
        params: EmptyParamsSchema,
        result: z.object({
            integrations: z.array(SpaceIntegrationSchema).nullable(),
            providers: z
                .array(z.object({ provider: z.string(), configured: z.boolean() }))
                .nullable()
                .optional(),
        }),
    },
    "integrations.bind": {
        verb: "POST",
        path: "/spaces/{spaceID}/integrations/{provider}/bind",
        params: z.strictObject({
            path: SpacePathSchema.extend({ provider }),
            body: z.strictObject({
                connection_id: IdentifierSchema,
                capability: z.enum(["calendar_read", "calendar_write"]),
            }),
        }),
        result: z.object({
            integration: SpaceIntegrationSchema,
            connection_id: z.string(),
            capability: z.string(),
        }),
    },
};
//# sourceMappingURL=connections.js.map