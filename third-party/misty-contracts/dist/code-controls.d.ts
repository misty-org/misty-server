import { z } from "zod";
export declare const MistyEditorPreferencesSchema: z.ZodObject<{
    autosaveDelayMs: z.ZodNumber;
    fontFamily: z.ZodString;
    fontSize: z.ZodNumber;
    formatOnSave: z.ZodBoolean;
    interfaceScale: z.ZodNumber;
    lineNumbers: z.ZodBoolean;
    tabSize: z.ZodNumber;
    theme: z.ZodString;
    wordWrap: z.ZodBoolean;
}, z.core.$strict>;
export declare const mistyCodeControlsContracts: {
    readonly "code.preferences.update": {
        readonly params: z.ZodDiscriminatedUnion<[z.ZodObject<{
            key: z.ZodLiteral<"font_size">;
            value: z.ZodNumber;
        }, z.core.$strict>, z.ZodObject<{
            key: z.ZodLiteral<"interface_scale">;
            value: z.ZodNumber;
        }, z.core.$strict>], "key">;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "code.models.open": {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "code.terminal.toggle": {
        readonly params: z.ZodObject<{
            placement: z.ZodDefault<z.ZodEnum<{
                down: "down";
                up: "up";
                left: "left";
                right: "right";
                current: "current";
            }>>;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "code.rewrite": {
        readonly params: z.ZodObject<{
            requestId: z.ZodString;
            instruction: z.ZodString;
            selection: z.ZodString;
            language: z.ZodString;
            filename: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodString;
    };
    readonly "code.rewrite.cancel": {
        readonly params: z.ZodObject<{
            requestId: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyCodeControlsMethod = keyof typeof mistyCodeControlsContracts;
export type MistyCodeControlsParams<M extends MistyCodeControlsMethod> = z.input<(typeof mistyCodeControlsContracts)[M]["params"]>;
export type MistyCodeControlsResult<M extends MistyCodeControlsMethod> = z.output<(typeof mistyCodeControlsContracts)[M]["result"]>;
export declare function isMistyCodeControlsMethod(method: string): method is MistyCodeControlsMethod;
