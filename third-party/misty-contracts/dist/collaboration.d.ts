import { z } from "zod";
export declare const MISTY_COLLABORATION_SEND_BYTES: number;
export declare const MISTY_COLLABORATION_RECEIVE_BYTES: number;
export declare const MistyCollaborationRoleSchema: z.ZodEnum<{
    creator: "creator";
    editor: "editor";
    viewer: "viewer";
}>;
export declare const MistyCollaborationResourceSchema: z.ZodEnum<{
    note: "note";
    drawing: "drawing";
}>;
export declare const MistyCollaborationEventSchema: z.ZodDiscriminatedUnion<[z.ZodObject<{
    type: z.ZodLiteral<"open">;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"binary">;
    data: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"text">;
    data: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"error">;
    message: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"close">;
    code: z.ZodNumber;
    reason: z.ZodString;
}, z.core.$strict>], "type">;
/** The host keeps join tickets, server URLs, sockets and their lifetime. */
export declare const mistyCollaborationContracts: {
    readonly "collaboration.open": {
        readonly params: z.ZodObject<{
            resource: z.ZodEnum<{
                note: "note";
                drawing: "drawing";
            }>;
            resourceId: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            handle: z.ZodString;
            role: z.ZodEnum<{
                creator: "creator";
                editor: "editor";
                viewer: "viewer";
            }>;
        }, z.core.$strict>;
    };
    readonly "collaboration.send": {
        readonly params: z.ZodObject<{
            handle: z.ZodString;
            data: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "collaboration.close": {
        readonly params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyCollaborationMethod = keyof typeof mistyCollaborationContracts;
export type MistyCollaborationParams<M extends MistyCollaborationMethod> = z.input<(typeof mistyCollaborationContracts)[M]["params"]>;
export type MistyCollaborationResult<M extends MistyCollaborationMethod> = z.output<(typeof mistyCollaborationContracts)[M]["result"]>;
export type MistyCollaborationEvent = z.infer<typeof MistyCollaborationEventSchema>;
export type MistyCollaborationResource = z.infer<typeof MistyCollaborationResourceSchema>;
export declare function isMistyCollaborationMethod(method: string): method is MistyCollaborationMethod;
