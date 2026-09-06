import { z } from "zod";
export declare const MISTY_LSP_MAX_BYTES: number;
export declare const MistyLspPayloadSchema: z.ZodString;
export declare const MistyLspLanguageSchema: z.ZodEnum<{
    typescript: "typescript";
    javascript: "javascript";
    rust: "rust";
    python: "python";
    go: "go";
    cpp: "cpp";
    c: "c";
    yaml: "yaml";
    json: "json";
    html: "html";
    css: "css";
    bash: "bash";
    lua: "lua";
    zig: "zig";
    tailwind: "tailwind";
}>;
export declare const MistyLspEventSchema: z.ZodDiscriminatedUnion<[z.ZodObject<{
    type: z.ZodLiteral<"message">;
    payload: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"exit">;
    reason: z.ZodString;
}, z.core.$strict>], "type">;
export declare const mistyCodeLspContracts: {
    readonly "code.lsp.start": {
        capability: "code.execute";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            language: z.ZodEnum<{
                typescript: "typescript";
                javascript: "javascript";
                rust: "rust";
                python: "python";
                go: "go";
                cpp: "cpp";
                c: "c";
                yaml: "yaml";
                json: "json";
                html: "html";
                css: "css";
                bash: "bash";
                lua: "lua";
                zig: "zig";
                tailwind: "tailwind";
            }>;
            cwd: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "code.lsp.send": {
        capability: "code.execute";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
            payload: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "code.lsp.stop": {
        capability: "code.execute";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyLspLanguage = z.infer<typeof MistyLspLanguageSchema>;
export type MistyLspEvent = z.infer<typeof MistyLspEventSchema>;
export declare function isMistyCodeLspMethod(method: string): method is keyof typeof mistyCodeLspContracts;
