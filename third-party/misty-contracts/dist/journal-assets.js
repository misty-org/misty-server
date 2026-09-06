import { z } from "zod";
import { DrawingPathSchema, IdentifierSchema, NotePathSchema } from "./requests.js";
export const MISTY_JOURNAL_ASSET_MAX_BYTES = 15 * 1024 * 1024;
export const JournalAssetMimeSchema = z.enum(["image/png", "image/jpeg", "image/webp", "image/gif", "image/avif", "image/bmp", "image/x-icon", "image/vnd.microsoft.icon"]);
const byteSize = z.number().int().min(1).max(MISTY_JOURNAL_ASSET_MAX_BYTES);
const sha256 = z.string().regex(/^[a-f0-9]{64}$/);
const filename = z.string().min(1).max(1024);
const httpsUrl = z.url().max(16384).refine((value) => {
    const url = new URL(value);
    return url.protocol === "https:" && !url.username && !url.password && !url.hash;
});
const headers = z.record(z.string().min(1).max(128), z.string().max(16384));
export const JournalAssetUploadInputSchema = z.strictObject({
    filename, mime_type: JournalAssetMimeSchema, byte_size: byteSize, sha256,
});
/** Host-only wire descriptors. Downloaded components use journal.assets instead. */
export const JournalAssetReservationSchema = z.object({
    upload: z.object({ id: IdentifierSchema }),
    transfer: z.object({
        url: httpsUrl, method: z.literal("PUT"), headers, expires_at: z.iso.datetime({ offset: true }),
    }),
    finalize: z.object({ headers }),
});
export const JournalAssetRecordSchema = z.object({
    id: IdentifierSchema, mime_type: JournalAssetMimeSchema, byte_size: byteSize, sha256,
});
export const JournalAssetDownloadSchema = z.object({
    url: httpsUrl, expires_at: z.iso.datetime({ offset: true }),
    filename, mime_type: JournalAssetMimeSchema, byte_size: byteSize, sha256,
});
export const mistyJournalAssetServerContracts = {
    "notes.assets.reserve": {
        verb: "POST", path: "/spaces/{spaceID}/notes/{noteID}/assets/uploads",
        params: z.strictObject({ path: NotePathSchema, body: JournalAssetUploadInputSchema }),
        result: JournalAssetReservationSchema,
    },
    "notes.assets.finalize": {
        verb: "POST", path: "/spaces/{spaceID}/notes/{noteID}/assets/uploads/{uploadID}/finalize",
        params: z.strictObject({ path: NotePathSchema.extend({ uploadID: IdentifierSchema }) }),
        result: z.object({ note_asset: JournalAssetRecordSchema }),
    },
    "notes.assets.download": {
        verb: "GET", path: "/spaces/{spaceID}/notes/{noteID}/assets/{assetID}/download",
        params: z.strictObject({ path: NotePathSchema.extend({ assetID: IdentifierSchema }) }),
        result: JournalAssetDownloadSchema,
    },
    "drawings.assets.reserve": {
        verb: "POST", path: "/spaces/{spaceID}/drawings/{drawingID}/assets/uploads",
        params: z.strictObject({ path: DrawingPathSchema, body: JournalAssetUploadInputSchema.extend({ file_id: IdentifierSchema }) }),
        result: JournalAssetReservationSchema,
    },
    "drawings.assets.finalize": {
        verb: "POST", path: "/spaces/{spaceID}/drawings/{drawingID}/assets/uploads/{uploadID}/finalize",
        params: z.strictObject({ path: DrawingPathSchema.extend({ uploadID: IdentifierSchema }) }),
        result: z.object({ drawing_asset: JournalAssetRecordSchema.extend({ excalidraw_file_id: IdentifierSchema }) }),
    },
    "drawings.assets.download": {
        verb: "GET", path: "/spaces/{spaceID}/drawings/{drawingID}/assets/{assetID}/download",
        params: z.strictObject({ path: DrawingPathSchema.extend({ assetID: IdentifierSchema }) }),
        result: JournalAssetDownloadSchema,
    },
};
export const MISTY_JOURNAL_ASSET_CHUNK_BYTES = 256 * 1024;
const handle = z.strictObject({ handle: z.uuid() });
const target = z.strictObject({ resource: z.enum(["note", "drawing"]), resourceId: IdentifierSchema });
const offset = z.number().int().min(0).max(MISTY_JOURNAL_ASSET_MAX_BYTES);
const data = z.string().max(Math.ceil(MISTY_JOURNAL_ASSET_CHUNK_BYTES / 3) * 4)
    .regex(/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/);
const empty = z.union([z.null(), z.undefined()]).transform(() => undefined);
export const mistyJournalAssetContracts = {
    "journal.assets.begin": {
        params: target.extend({ filename, mimeType: JournalAssetMimeSchema, bytes: byteSize, externalFileId: IdentifierSchema.optional() })
            .refine(value => value.resource !== "drawing" || !!value.externalFileId, "Drawings require an external file ID."),
        result: handle,
    },
    "journal.assets.write": { params: handle.extend({ offset, data }), result: empty },
    "journal.assets.commit": { params: handle, result: JournalAssetRecordSchema.extend({ excalidraw_file_id: IdentifierSchema.optional() }) },
    "journal.assets.open": {
        params: target.extend({ assetId: IdentifierSchema }),
        result: handle.extend({ filename, mimeType: JournalAssetMimeSchema, bytes: byteSize, sha256 }),
    },
    "journal.assets.read": {
        params: handle.extend({ offset, length: z.number().int().min(1).max(MISTY_JOURNAL_ASSET_CHUNK_BYTES) }),
        result: z.strictObject({ data }),
    },
    "journal.assets.close": { params: handle, result: empty },
};
export function isMistyJournalAssetMethod(method) {
    return Object.hasOwn(mistyJournalAssetContracts, method);
}
//# sourceMappingURL=journal-assets.js.map