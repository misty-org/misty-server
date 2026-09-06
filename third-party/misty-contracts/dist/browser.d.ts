import { z } from "zod";
export declare const MistyBrowserUrlSchema: z.ZodString;
/** Viewport CSS pixels. The host clips to the App view and applies native zoom. */
export declare const MistyBrowserBoundsSchema: z.ZodObject<{
    x: z.ZodNumber;
    y: z.ZodNumber;
    width: z.ZodNumber;
    height: z.ZodNumber;
}, z.core.$strict>;
export declare const MistyBrowserHandleSchema: z.ZodString;
export declare const MistyBrowserInspectionSchema: z.ZodObject<{
    documentId: z.ZodString;
    url: z.ZodString;
    title: z.ZodString;
    text: z.ZodString;
    truncated: z.ZodBoolean;
    interactive: z.ZodArray<z.ZodObject<{
        ref: z.ZodString;
        tag: z.ZodString;
        role: z.ZodString;
        name: z.ZodString;
    }, z.core.$strict>>;
    contentTrust: z.ZodLiteral<"untrusted-web-page">;
}, z.core.$strict>;
export declare const MistyBrowserEventSchema: z.ZodDiscriminatedUnion<[z.ZodObject<{
    type: z.ZodLiteral<"page">;
    phase: z.ZodEnum<{
        started: "started";
        finished: "finished";
    }>;
    url: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"title">;
    title: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"favicon">;
    url: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"compatibility">;
    kind: z.ZodLiteral<"cloudflare_challenge">;
    url: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"layout">;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"state">;
    canBack: z.ZodBoolean;
    canForward: z.ZodBoolean;
    loading: z.ZodBoolean;
    agentAccess: z.ZodBoolean;
    history: z.ZodArray<z.ZodString>;
    error: z.ZodNullable<z.ZodString>;
    notice: z.ZodNullable<z.ZodString>;
}, z.core.$strict>], "type">;
export declare const mistyBrowserContracts: {
    readonly "browser.create": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            url: z.ZodOptional<z.ZodString>;
            bounds: z.ZodObject<{
                x: z.ZodNumber;
                y: z.ZodNumber;
                width: z.ZodNumber;
                height: z.ZodNumber;
            }, z.core.$strict>;
            nativeLiveResize: z.ZodDefault<z.ZodBoolean>;
        }, z.core.$strict>;
        result: z.ZodObject<{
            handle: z.ZodString;
            contextId: z.ZodString;
            url: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "browser.layout": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
            bounds: z.ZodObject<{
                x: z.ZodNumber;
                y: z.ZodNumber;
                width: z.ZodNumber;
                height: z.ZodNumber;
            }, z.core.$strict>;
            visible: z.ZodBoolean;
            nativeLiveResize: z.ZodDefault<z.ZodBoolean>;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.navigate": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
            url: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.back": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.forward": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.reload": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.close": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.inspect": {
        readonly capability: "browser.inspect";
        readonly platforms: readonly ["macos"];
        readonly params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            documentId: z.ZodString;
            url: z.ZodString;
            title: z.ZodString;
            text: z.ZodString;
            truncated: z.ZodBoolean;
            interactive: z.ZodArray<z.ZodObject<{
                ref: z.ZodString;
                tag: z.ZodString;
                role: z.ZodString;
                name: z.ZodString;
            }, z.core.$strict>>;
            contentTrust: z.ZodLiteral<"untrusted-web-page">;
        }, z.core.$strict>;
    };
    readonly "browser.click": {
        readonly capability: "browser.interact";
        readonly platforms: readonly ["macos"];
        readonly params: z.ZodObject<{
            handle: z.ZodString;
            documentId: z.ZodString;
            elementRef: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.overlay": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
            reason: z.ZodString;
            active: z.ZodBoolean;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyBrowserMethod = keyof typeof mistyBrowserContracts;
export type MistyBrowserParams<M extends MistyBrowserMethod> = z.input<(typeof mistyBrowserContracts)[M]["params"]>;
export type MistyBrowserResult<M extends MistyBrowserMethod> = z.output<(typeof mistyBrowserContracts)[M]["result"]>;
export type MistyBrowserEvent = z.infer<typeof MistyBrowserEventSchema>;
export type MistyBrowserBounds = z.infer<typeof MistyBrowserBoundsSchema>;
export type MistyBrowserInspection = z.infer<typeof MistyBrowserInspectionSchema>;
export declare function isMistyBrowserMethod(method: string): method is MistyBrowserMethod;
