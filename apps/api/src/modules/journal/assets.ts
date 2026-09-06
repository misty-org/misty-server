import { z } from "zod";
import { JournalError, type JournalActor } from "./access.js";
import { loadAssetConfig } from "./asset-config.js";
import { AssetError, type AssetKind, type AssetInput, type createAssetRepository } from "./asset-repository.js";
import { StorageUnavailable, storageFilename, type ObjectStore, type ObjectMetadata } from "../storage/object-store.js";

export const assetInput = z.object({ filename: z.string().max(1024).transform(storageFilename).pipe(z.string().min(1)),
  mime_type: z.enum(["image/png", "image/jpeg", "image/webp", "image/gif", "image/avif", "image/bmp", "image/x-icon", "image/vnd.microsoft.icon"]),
  byte_size: z.number().int().min(1).max(15 * 1024 * 1024), sha256: z.string().regex(/^[a-f0-9]{64}$/) }).strict();
export const drawingAssetInput = assetInput.extend({ file_id: z.string().regex(/^[A-Za-z0-9_-]{1,160}$/) });

export function createJournalAssets(repository: ReturnType<typeof createAssetRepository>, store: ObjectStore | null, now = () => new Date(), config = loadAssetConfig({})) {
  function storage() { if (!store) throw new StorageUnavailable(); return store; }
  return {
    list: repository.list,
    remove: repository.remove,
    async reserve(actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string, input: AssetInput) {
      const objects = storage();
      if (input.byte_size > (kind === "note" ? config.noteMaxBytes : config.drawingMaxBytes)) throw new JournalError("invalid_request");
      const { upload, publicUpload, token } = await repository.reserve(actor, spaceId, kind, parentId, input);
      const transfer = await objects.signUpload(upload.object_key,
        { byteSize: input.byte_size, mimeType: input.mime_type, sha256: input.sha256 }, new Date(Math.min(now().getTime() + config.uploadTtlMs, upload.expires_at.getTime())));
      // The private upload token and object key never appear on the upload row.
      return { upload: publicUpload,
        transfer, finalize: { headers: { "X-Misty-Library-Upload-Token": token } } };
    },
    async finalize(actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string, uploadId: string, token: string, signal?: AbortSignal) {
      const objects = storage();
      await repository.inspect(actor, spaceId, kind, parentId, uploadId, token);
      const verified = new Map<string, ObjectMetadata | null>();
      // Each pass rechecks the exact credential, audience and upload under locks.
      // A changed deduplication target requests another external HEAD, never a
      // network call while holding the transaction open.
      for (let attempt = 0; attempt < 4; attempt++) {
        const outcome = await repository.complete(actor, spaceId, kind, parentId, uploadId, token, verified);
        if ("result" in outcome) return outcome.result;
        if ("mismatch" in outcome) throw new AssetError("upload_mismatch");
        verified.set(outcome.needHead, await objects.head(outcome.needHead, signal));
      }
      throw new StorageUnavailable();
    },
    async download(actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string, assetId: string) {
      const objects = storage(), asset = await repository.download(actor, spaceId, kind, parentId, assetId);
      const transfer = await objects.signDownload(asset.object_key, asset.filename, new Date(now().getTime() + config.downloadTtlMs));
      return { ...transfer, mime_type: asset.mime_type, byte_size: asset.byte_size, sha256: asset.sha256 };
    },
  };
}
