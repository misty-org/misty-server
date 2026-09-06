import { z } from "zod";
const handle = z.string().min(1).max(256);
// Native entry tokens preserve filenames losslessly; Apps must not decode or construct paths.
const entry = z.string().regex(/^[uw]:[A-Za-z0-9_-]{2,4096}$/);
export const MistyDirectoryEntrySchema = z.strictObject({
    entry,
    name: z.string().max(1024),
    kind: z.enum(["file", "directory", "symlink", "other"]),
    bytes: z.number().int().nonnegative().safe().optional(),
});
export const mistyDirectoryContracts = {
    "files.openTrash": {
        capability: "files.write",
        platforms: ["macos"],
        params: z.strictObject({}),
        result: z.strictObject({ handle, name: z.literal("Trash"), writable: z.literal(true) }),
    },
    "files.listSavedDirectories": {
        params: z.strictObject({}),
        result: z.array(z.strictObject({ bookmarkId: z.string().uuid(), name: z.string().max(1024), writable: z.boolean() })).max(32),
    },
    "files.rememberDirectory": {
        params: z.strictObject({ directory: handle, write: z.boolean().default(false) }),
        result: z.strictObject({ bookmarkId: z.string().uuid(), name: z.string().max(1024), writable: z.boolean() }),
    },
    "files.reopenDirectory": {
        params: z.strictObject({ bookmarkId: z.string().uuid(), write: z.boolean().default(false) }),
        result: z.strictObject({ handle, name: z.string().max(1024), writable: z.boolean() }),
    },
    "files.forgetDirectory": {
        params: z.strictObject({ bookmarkId: z.string().uuid() }),
        result: z.null(),
    },
    "files.shareDirectory": {
        params: z.strictObject({ directory: handle, write: z.boolean().default(false) }),
        result: z.strictObject({ ticket: z.string().uuid(), expiresInMs: z.literal(60000) }),
    },
    "files.adoptDirectory": {
        params: z.strictObject({ ticket: z.string().uuid(), write: z.boolean().default(false) }),
        result: z.strictObject({ handle, name: z.string().max(1024), writable: z.boolean() }),
    },
    "files.cancelDirectoryShare": {
        params: z.strictObject({ ticket: z.string().uuid() }),
        result: z.null(),
    },
    "files.listDirectory": {
        params: z.strictObject({
            directory: handle,
            offset: z.number().int().min(0).max(1000000).default(0),
            limit: z.number().int().min(1).max(200).default(200),
        }),
        result: z.strictObject({
            entries: z.array(MistyDirectoryEntrySchema).max(200),
            nextOffset: z.number().int().min(0).max(1000000).nullable(),
        }),
    },
    "files.openEntry": {
        params: z.strictObject({
            directory: handle,
            entry,
            write: z.boolean().default(false),
        }),
        result: z.strictObject({
            handle,
            name: z.string().max(1024),
            kind: z.enum(["file", "directory"]),
            bytes: z.number().int().nonnegative().safe().optional(),
        }),
    },
};
const encoder = new TextEncoder();
const newName = z
    .string()
    .min(1)
    .max(255)
    .refine((name) => name !== "." &&
    name !== ".." &&
    !name.includes("/") &&
    !name.includes("\0") &&
    encoder.encode(name).byteLength <= 255, "Expected a single filename of at most 255 UTF-8 bytes.");
const mutation = (params, result) => ({
    capability: "files.write",
    platforms: ["macos"],
    params,
    result,
});
export const mistyDirectoryMutationContracts = {
    "files.createEntry": mutation(z.strictObject({
        directory: handle,
        name: newName,
        kind: z.enum(["file", "directory"]),
    }), z.strictObject({
        entry,
        name: newName,
        kind: z.enum(["file", "directory"]),
    })),
    "files.renameEntry": mutation(z.strictObject({ directory: handle, entry, name: newName }), z.strictObject({ entry, name: newName })),
    "files.removeEntry": mutation(z.strictObject({
        directory: handle,
        entry,
        recursive: z.boolean().default(false),
    }), z.union([z.null(), z.undefined()]).transform(() => undefined)),
};
//# sourceMappingURL=directories.js.map