import { z } from "zod";
export declare const MistyFileMetadataSchema: z.ZodObject<{
    kind: z.ZodEnum<{
        file: "file";
        directory: "directory";
    }>;
    bytes: z.ZodNumber;
    modifiedMs: z.ZodNullable<z.ZodNumber>;
    createdMs: z.ZodNullable<z.ZodNumber>;
    readOnly: z.ZodBoolean;
    writeGranted: z.ZodBoolean;
}, z.core.$strict>;
export declare const mistyFileObservationContracts: {
    readonly "files.stat": {
        capability: "files.read";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodObject<{
            kind: z.ZodEnum<{
                file: "file";
                directory: "directory";
            }>;
            bytes: z.ZodNumber;
            modifiedMs: z.ZodNullable<z.ZodNumber>;
            createdMs: z.ZodNullable<z.ZodNumber>;
            readOnly: z.ZodBoolean;
            writeGranted: z.ZodBoolean;
        }, z.core.$strict>;
    };
    readonly "files.watchDirectory": {
        capability: "files.read";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            directory: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodObject<{
            watcher: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "files.watchStatus": {
        capability: "files.read";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            watcher: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodObject<{
            revision: z.ZodNumber;
            active: z.ZodBoolean;
            reason: z.ZodNullable<z.ZodEnum<{
                root_changed: "root_changed";
                watch_failed: "watch_failed";
            }>>;
        }, z.core.$strict>;
    };
    readonly "files.watchClose": {
        capability: "files.read";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            watcher: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyFileMetadata = z.output<typeof MistyFileMetadataSchema>;
export type MistyDirectoryWatchStatus = z.output<typeof mistyFileObservationContracts["files.watchStatus"]["result"]>;
