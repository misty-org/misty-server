import { z } from "zod";
export declare const MistySSHConnectionSchema: z.ZodDiscriminatedUnion<[z.ZodObject<{
    kind: z.ZodLiteral<"configured">;
    id: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"direct">;
    host: z.ZodString;
    user: z.ZodOptional<z.ZodString>;
    port: z.ZodNumber;
}, z.core.$strict>], "kind">;
export declare const MistyTerminalSizeSchema: z.ZodObject<{
    cols: z.ZodNumber;
    rows: z.ZodNumber;
    pixelWidth: z.ZodOptional<z.ZodNumber>;
    pixelHeight: z.ZodOptional<z.ZodNumber>;
}, z.core.$strict>;
export declare const MistyTerminalCreateSchema: z.ZodObject<{
    cols: z.ZodOptional<z.ZodNumber>;
    rows: z.ZodOptional<z.ZodNumber>;
    pixelWidth: z.ZodOptional<z.ZodOptional<z.ZodNumber>>;
    pixelHeight: z.ZodOptional<z.ZodOptional<z.ZodNumber>>;
    cwd: z.ZodOptional<z.ZodString>;
    env: z.ZodOptional<z.ZodRecord<z.ZodString, z.ZodString>>;
    environment: z.ZodOptional<z.ZodDiscriminatedUnion<[z.ZodObject<{
        kind: z.ZodLiteral<"local">;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"ssh">;
        connection: z.ZodDiscriminatedUnion<[z.ZodObject<{
            kind: z.ZodLiteral<"configured">;
            id: z.ZodString;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"direct">;
            host: z.ZodString;
            user: z.ZodOptional<z.ZodString>;
            port: z.ZodNumber;
        }, z.core.$strict>], "kind">;
    }, z.core.$strict>], "kind">>;
}, z.core.$strict>;
export declare const MistyTerminalSessionSchema: z.ZodObject<{
    handle: z.ZodString;
}, z.core.$strict>;
export declare const MistyTerminalEventSchema: z.ZodDiscriminatedUnion<[z.ZodObject<{
    type: z.ZodLiteral<"output">;
    data: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"exit">;
    exitCode: z.ZodNullable<z.ZodNumber>;
}, z.core.$strict>], "type">;
export declare const MistySSHEnvironmentSchema: z.ZodObject<{
    id: z.ZodString;
    label: z.ZodString;
    host: z.ZodString;
    user: z.ZodOptional<z.ZodNullable<z.ZodString>>;
    port: z.ZodNumber;
    deviceLocal: z.ZodBoolean;
    agentTools: z.ZodString;
}, z.core.$strip>;
export declare const MistySSHHostKeyStatusSchema: z.ZodObject<{
    state: z.ZodString;
    fingerprints: z.ZodArray<z.ZodString>;
    message: z.ZodString;
}, z.core.$strip>;
export declare const mistyTerminalContracts: {
    readonly "terminal.create": {
        capability: "terminal.execute";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            cols: z.ZodOptional<z.ZodNumber>;
            rows: z.ZodOptional<z.ZodNumber>;
            pixelWidth: z.ZodOptional<z.ZodOptional<z.ZodNumber>>;
            pixelHeight: z.ZodOptional<z.ZodOptional<z.ZodNumber>>;
            cwd: z.ZodOptional<z.ZodString>;
            env: z.ZodOptional<z.ZodRecord<z.ZodString, z.ZodString>>;
            environment: z.ZodOptional<z.ZodDiscriminatedUnion<[z.ZodObject<{
                kind: z.ZodLiteral<"local">;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"ssh">;
                connection: z.ZodDiscriminatedUnion<[z.ZodObject<{
                    kind: z.ZodLiteral<"configured">;
                    id: z.ZodString;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"direct">;
                    host: z.ZodString;
                    user: z.ZodOptional<z.ZodString>;
                    port: z.ZodNumber;
                }, z.core.$strict>], "kind">;
            }, z.core.$strict>], "kind">>;
        }, z.core.$strict>;
        result: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "terminal.write": {
        capability: "terminal.execute";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
            data: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "terminal.resize": {
        capability: "terminal.execute";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            cols: z.ZodNumber;
            rows: z.ZodNumber;
            pixelWidth: z.ZodOptional<z.ZodNumber>;
            pixelHeight: z.ZodOptional<z.ZodNumber>;
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "terminal.close": {
        capability: "terminal.execute";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "terminal.environments": {
        capability: "terminal.execute";
        platforms: readonly ["macos"];
        params: z.ZodObject<{}, z.core.$strict>;
        result: z.ZodArray<z.ZodObject<{
            id: z.ZodString;
            label: z.ZodString;
            host: z.ZodString;
            user: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            port: z.ZodNumber;
            deviceLocal: z.ZodBoolean;
            agentTools: z.ZodString;
        }, z.core.$strip>>;
    };
    readonly "terminal.preflight": {
        capability: "terminal.execute";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            connection: z.ZodDiscriminatedUnion<[z.ZodObject<{
                kind: z.ZodLiteral<"configured">;
                id: z.ZodString;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"direct">;
                host: z.ZodString;
                user: z.ZodOptional<z.ZodString>;
                port: z.ZodNumber;
            }, z.core.$strict>], "kind">;
        }, z.core.$strict>;
        result: z.ZodObject<{
            state: z.ZodString;
            fingerprints: z.ZodArray<z.ZodString>;
            message: z.ZodString;
        }, z.core.$strip>;
    };
    readonly "terminal.trustHost": {
        capability: "terminal.execute";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            connection: z.ZodDiscriminatedUnion<[z.ZodObject<{
                kind: z.ZodLiteral<"configured">;
                id: z.ZodString;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"direct">;
                host: z.ZodString;
                user: z.ZodOptional<z.ZodString>;
                port: z.ZodNumber;
            }, z.core.$strict>], "kind">;
            fingerprint: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodObject<{
            state: z.ZodString;
            fingerprints: z.ZodArray<z.ZodString>;
            message: z.ZodString;
        }, z.core.$strip>;
    };
};
export type MistyTerminalMethod = keyof typeof mistyTerminalContracts;
export type MistyTerminalParams<M extends MistyTerminalMethod> = z.input<(typeof mistyTerminalContracts)[M]["params"]>;
export type MistyTerminalResult<M extends MistyTerminalMethod> = z.output<(typeof mistyTerminalContracts)[M]["result"]>;
export type MistySSHConnection = z.input<typeof MistySSHConnectionSchema>;
export type MistyTerminalSize = z.input<typeof MistyTerminalSizeSchema>;
export type MistyTerminalCreate = z.input<typeof MistyTerminalCreateSchema>;
export type MistyTerminalSession = Readonly<z.output<typeof MistyTerminalSessionSchema>>;
export type MistyTerminalEvent = z.output<typeof MistyTerminalEventSchema>;
export type MistySSHEnvironment = z.output<typeof MistySSHEnvironmentSchema>;
export type MistySSHHostKeyStatus = z.output<typeof MistySSHHostKeyStatusSchema>;
export declare function isMistyTerminalMethod(method: string): method is MistyTerminalMethod;
export declare function parseTerminalParams<M extends MistyTerminalMethod>(method: M, input: unknown): MistyTerminalParams<M>;
export declare function parseTerminalResult<M extends MistyTerminalMethod>(method: M, input: unknown): MistyTerminalResult<M>;
