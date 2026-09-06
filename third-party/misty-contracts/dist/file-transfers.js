import { z } from "zod";
const handle = z.string().min(1).max(256);
const entry = z.string().regex(/^[uw]:[A-Za-z0-9_-]{2,4096}$/);
const voidResult = z.union([z.null(), z.undefined()]).transform(() => undefined);
export const MistyFileTransferResultSchema = z.strictObject({
    entry, name: z.string().max(1024), kind: z.enum(["file", "directory", "symlink"]), sourceRemoved: z.boolean(),
});
export const MistyFileTransferStatusSchema = z.strictObject({
    status: z.enum(["running", "completed", "failed", "cancelled"]),
    bytes: z.number().int().nonnegative().safe(), files: z.number().int().nonnegative().safe(),
    message: z.string().max(2048), result: MistyFileTransferResultSchema.nullable(),
});
const local = (params, result) => ({
    capability: "files.write", platforms: ["macos"], params, result,
});
export const mistyFileTransferContracts = {
    "files.transferStart": local(z.strictObject({
        sourceDirectory: handle, entry, destinationDirectory: handle,
        operation: z.enum(["copy", "move"]), conflict: z.enum(["error", "rename"]).default("error"),
    }), z.strictObject({ jobId: handle })),
    "files.transferStatus": local(z.strictObject({ jobId: handle }), MistyFileTransferStatusSchema),
    "files.transferCancel": local(z.strictObject({ jobId: handle }), voidResult),
    "files.transferClose": local(z.strictObject({ jobId: handle }), voidResult),
};
//# sourceMappingURL=file-transfers.js.map