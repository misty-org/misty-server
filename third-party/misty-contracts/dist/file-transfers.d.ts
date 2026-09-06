import { z } from "zod";
export declare const MistyFileTransferResultSchema: z.ZodObject<{
    entry: z.ZodString;
    name: z.ZodString;
    kind: z.ZodEnum<{
        file: "file";
        directory: "directory";
        symlink: "symlink";
    }>;
    sourceRemoved: z.ZodBoolean;
}, z.core.$strict>;
export declare const MistyFileTransferStatusSchema: z.ZodObject<{
    status: z.ZodEnum<{
        failed: "failed";
        running: "running";
        completed: "completed";
        cancelled: "cancelled";
    }>;
    bytes: z.ZodNumber;
    files: z.ZodNumber;
    message: z.ZodString;
    result: z.ZodNullable<z.ZodObject<{
        entry: z.ZodString;
        name: z.ZodString;
        kind: z.ZodEnum<{
            file: "file";
            directory: "directory";
            symlink: "symlink";
        }>;
        sourceRemoved: z.ZodBoolean;
    }, z.core.$strict>>;
}, z.core.$strict>;
export declare const mistyFileTransferContracts: {
    readonly "files.transferStart": {
        capability: "files.write";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            sourceDirectory: z.ZodString;
            entry: z.ZodString;
            destinationDirectory: z.ZodString;
            operation: z.ZodEnum<{
                copy: "copy";
                move: "move";
            }>;
            conflict: z.ZodDefault<z.ZodEnum<{
                error: "error";
                rename: "rename";
            }>>;
        }, z.core.$strict>;
        result: z.ZodObject<{
            jobId: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "files.transferStatus": {
        capability: "files.write";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            jobId: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodObject<{
            status: z.ZodEnum<{
                failed: "failed";
                running: "running";
                completed: "completed";
                cancelled: "cancelled";
            }>;
            bytes: z.ZodNumber;
            files: z.ZodNumber;
            message: z.ZodString;
            result: z.ZodNullable<z.ZodObject<{
                entry: z.ZodString;
                name: z.ZodString;
                kind: z.ZodEnum<{
                    file: "file";
                    directory: "directory";
                    symlink: "symlink";
                }>;
                sourceRemoved: z.ZodBoolean;
            }, z.core.$strict>>;
        }, z.core.$strict>;
    };
    readonly "files.transferCancel": {
        capability: "files.write";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            jobId: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "files.transferClose": {
        capability: "files.write";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            jobId: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyFileTransferRequest = z.input<typeof mistyFileTransferContracts["files.transferStart"]["params"]>;
export type MistyFileTransferResult = z.output<typeof MistyFileTransferResultSchema>;
export type MistyFileTransferStatus = z.output<typeof MistyFileTransferStatusSchema>;
