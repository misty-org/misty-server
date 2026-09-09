import { z } from "zod";
export declare const ConnectedAccountSchema: z.ZodObject<{
    id: z.ZodString;
    provider: z.ZodString;
    account_id: z.ZodString;
    account_display: z.ZodString;
    capabilities: z.ZodNullable<z.ZodArray<z.ZodString>>;
    granted_scopes: z.ZodNullable<z.ZodArray<z.ZodString>>;
    status: z.ZodEnum<{
        active: "active";
        revoked: "revoked";
        needs_attention: "needs_attention";
    }>;
    last_error_code: z.ZodOptional<z.ZodString>;
    expires_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
}, z.core.$strip>;
export declare const SpaceIntegrationSchema: z.ZodObject<{
    id: z.ZodString;
    space_id: z.ZodString;
    provider: z.ZodString;
    display_name: z.ZodString;
    granted_permissions: z.ZodNullable<z.ZodArray<z.ZodString>>;
    status: z.ZodString;
    connected_by_user_id: z.ZodString;
    created_at: z.ZodString;
    updated_at: z.ZodString;
}, z.core.$strip>;
export declare const mistyConnectionContracts: {
    readonly "connections.list": {
        readonly verb: "GET";
        readonly path: "/connections";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            connections: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                provider: z.ZodString;
                account_id: z.ZodString;
                account_display: z.ZodString;
                capabilities: z.ZodNullable<z.ZodArray<z.ZodString>>;
                granted_scopes: z.ZodNullable<z.ZodArray<z.ZodString>>;
                status: z.ZodEnum<{
                    active: "active";
                    revoked: "revoked";
                    needs_attention: "needs_attention";
                }>;
                last_error_code: z.ZodOptional<z.ZodString>;
                expires_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            }, z.core.$strip>>>;
            providers: z.ZodOptional<z.ZodRecord<z.ZodString, z.ZodBoolean>>;
        }, z.core.$strip>;
    };
    readonly "connections.remove": {
        readonly verb: "DELETE";
        readonly path: "/connections/{connectionID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                connectionID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "connections.authorize": {
        readonly verb: "POST";
        readonly path: "/connections/{provider}/authorize";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                provider: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                capabilities: z.ZodArray<z.ZodString>;
                return_to: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            provider: z.ZodString;
            authorization_url: z.ZodURL;
            state_expires_at: z.ZodOptional<z.ZodString>;
        }, z.core.$strip>;
    };
    readonly "integrations.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/integrations";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            integrations: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                provider: z.ZodString;
                display_name: z.ZodString;
                granted_permissions: z.ZodNullable<z.ZodArray<z.ZodString>>;
                status: z.ZodString;
                connected_by_user_id: z.ZodString;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$strip>>>;
            providers: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
                provider: z.ZodString;
                configured: z.ZodBoolean;
            }, z.core.$strip>>>>;
        }, z.core.$strip>;
    };
    readonly "integrations.bind": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/integrations/{provider}/bind";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                provider: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                connection_id: z.ZodString;
                capability: z.ZodEnum<{
                    calendar_read: "calendar_read";
                    calendar_write: "calendar_write";
                }>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            integration: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                provider: z.ZodString;
                display_name: z.ZodString;
                granted_permissions: z.ZodNullable<z.ZodArray<z.ZodString>>;
                status: z.ZodString;
                connected_by_user_id: z.ZodString;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$strip>;
            connection_id: z.ZodString;
            capability: z.ZodString;
        }, z.core.$strip>;
    };
};
export type ConnectedAccount = z.output<typeof ConnectedAccountSchema>;
export type SpaceIntegration = z.output<typeof SpaceIntegrationSchema>;
