import { z } from "zod";
export declare const MistyDirectoryEntrySchema: z.ZodObject<{
    entry: z.ZodString;
    name: z.ZodString;
    kind: z.ZodEnum<{
        file: "file";
        directory: "directory";
        symlink: "symlink";
        other: "other";
    }>;
    bytes: z.ZodOptional<z.ZodNumber>;
}, z.core.$strict>;
export declare const mistyDirectoryContracts: {
    readonly "files.openTrash": {
        readonly capability: "files.write";
        readonly platforms: readonly ["macos"];
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodObject<{
            handle: z.ZodString;
            name: z.ZodLiteral<"Trash">;
            writable: z.ZodLiteral<true>;
        }, z.core.$strict>;
    };
    readonly "files.listSavedDirectories": {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodArray<z.ZodObject<{
            bookmarkId: z.ZodString;
            name: z.ZodString;
            writable: z.ZodBoolean;
        }, z.core.$strict>>;
    };
    readonly "files.rememberDirectory": {
        readonly params: z.ZodObject<{
            directory: z.ZodString;
            write: z.ZodDefault<z.ZodBoolean>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            bookmarkId: z.ZodString;
            name: z.ZodString;
            writable: z.ZodBoolean;
        }, z.core.$strict>;
    };
    readonly "files.reopenDirectory": {
        readonly params: z.ZodObject<{
            bookmarkId: z.ZodString;
            write: z.ZodDefault<z.ZodBoolean>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            handle: z.ZodString;
            name: z.ZodString;
            writable: z.ZodBoolean;
        }, z.core.$strict>;
    };
    readonly "files.forgetDirectory": {
        readonly params: z.ZodObject<{
            bookmarkId: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodNull;
    };
    readonly "files.shareDirectory": {
        readonly params: z.ZodObject<{
            directory: z.ZodString;
            write: z.ZodDefault<z.ZodBoolean>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            ticket: z.ZodString;
            expiresInMs: z.ZodLiteral<60000>;
        }, z.core.$strict>;
    };
    readonly "files.adoptDirectory": {
        readonly params: z.ZodObject<{
            ticket: z.ZodString;
            write: z.ZodDefault<z.ZodBoolean>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            handle: z.ZodString;
            name: z.ZodString;
            writable: z.ZodBoolean;
        }, z.core.$strict>;
    };
    readonly "files.cancelDirectoryShare": {
        readonly params: z.ZodObject<{
            ticket: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodNull;
    };
    readonly "files.listDirectory": {
        readonly params: z.ZodObject<{
            directory: z.ZodString;
            offset: z.ZodDefault<z.ZodNumber>;
            limit: z.ZodDefault<z.ZodNumber>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            entries: z.ZodArray<z.ZodObject<{
                entry: z.ZodString;
                name: z.ZodString;
                kind: z.ZodEnum<{
                    file: "file";
                    directory: "directory";
                    symlink: "symlink";
                    other: "other";
                }>;
                bytes: z.ZodOptional<z.ZodNumber>;
            }, z.core.$strict>>;
            nextOffset: z.ZodNullable<z.ZodNumber>;
        }, z.core.$strict>;
    };
    readonly "files.openEntry": {
        readonly params: z.ZodObject<{
            directory: z.ZodString;
            entry: z.ZodString;
            write: z.ZodDefault<z.ZodBoolean>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            handle: z.ZodString;
            name: z.ZodString;
            kind: z.ZodEnum<{
                file: "file";
                directory: "directory";
            }>;
            bytes: z.ZodOptional<z.ZodNumber>;
        }, z.core.$strict>;
    };
};
export type MistyDirectoryEntry = z.output<typeof MistyDirectoryEntrySchema>;
export type MistyDirectoryListing = z.output<(typeof mistyDirectoryContracts)["files.listDirectory"]["result"]>;
export type MistyOpenedEntry = z.output<(typeof mistyDirectoryContracts)["files.openEntry"]["result"]>;
export declare const mistyDirectoryMutationContracts: {
    readonly "files.createEntry": {
        capability: "files.write";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            directory: z.ZodString;
            name: z.ZodString;
            kind: z.ZodEnum<{
                file: "file";
                directory: "directory";
            }>;
        }, z.core.$strict>;
        result: z.ZodObject<{
            entry: z.ZodString;
            name: z.ZodString;
            kind: z.ZodEnum<{
                file: "file";
                directory: "directory";
            }>;
        }, z.core.$strict>;
    };
    readonly "files.renameEntry": {
        capability: "files.write";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            directory: z.ZodString;
            entry: z.ZodString;
            name: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodObject<{
            entry: z.ZodString;
            name: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "files.removeEntry": {
        capability: "files.write";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            directory: z.ZodString;
            entry: z.ZodString;
            recursive: z.ZodDefault<z.ZodBoolean>;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
