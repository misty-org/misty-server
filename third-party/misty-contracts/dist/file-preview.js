import { z } from "zod";
export const MistyArchiveFormatSchema = z.enum(["zip", "tar", "7z", "rar"]);
export const MistyArchivePreviewSchema = z.strictObject({
    format: MistyArchiveFormatSchema,
    entries: z.array(z.strictObject({
        path: z.string().max(16384),
        isDir: z.boolean(),
        compressedSize: z.number().int().nonnegative(),
        uncompressedSize: z.number().int().nonnegative(),
    })).max(500),
});
export const mistyFilePreviewContracts = {
    "files.listArchive": {
        params: z.strictObject({ handle: z.string().min(1).max(256), format: MistyArchiveFormatSchema }),
        result: MistyArchivePreviewSchema,
    },
};
//# sourceMappingURL=file-preview.js.map