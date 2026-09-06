import { extname } from "node:path";
import { SpaceError } from "../spaces/model.js";
import { downloadDisposition, storageFilename, StorageUnavailable, type ObjectStore, type StreamingObjectStore } from "../storage/object-store.js";
import type { createEgressGuard } from "../storage/egress.js";
import type { createLibraryDownloadRepository } from "./download-repository.js";
export class LibraryObjectMismatch extends Error {}
export function createLibraryDownloads(options: {
  repository: ReturnType<typeof createLibraryDownloadRepository>;
  store: (Partial<Pick<ObjectStore, "signDownload">> & Partial<StreamingObjectStore>) | null;
  egress: ReturnType<typeof createEgressGuard>; downloadTtlMs: number; now?: () => Date;
}) {
  return async (userId: string, spaceId: string, itemId: string, original: boolean, token: string, signal: AbortSignal) => {
    const source = await options.repository.resolve(userId, spaceId, itemId, original, token);
    options.egress.charge(userId, source.byteSize);
    const filename = storageFilename(source.rendition ? renditionFilename(source.filename, source.mimeType) : source.filename) || "download";
    const headers = { "Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff" };
    if (options.store?.signDownload) {
      const descriptor = await options.store.signDownload(source.objectKey, filename, new Date((options.now?.() ?? new Date()).getTime() + options.downloadTtlMs));
      return Response.json(descriptor, { headers: { ...headers, "X-Misty-Signed-Download": "1" } });
    }
    if (!options.store?.openStream) throw new StorageUnavailable();
    const object = await options.store.openStream(source.objectKey, signal);
    if (!object) throw new SpaceError("not_found");
    if (object.metadata.byteSize !== source.byteSize || object.metadata.sha256 !== source.sha256) {
      await object.body.cancel().catch(() => {}); throw new LibraryObjectMismatch();
    }
    return new Response(object.body, { headers: { ...headers, "Content-Type": source.mimeType, "Content-Length": String(source.byteSize), "Content-Disposition": downloadDisposition(filename) } });
  };
}
function renditionFilename(filename: string, mimeType: string) {
  const extension = extname(filename), name = extension ? filename.slice(0, -extension.length) : filename;
  const base = name.trim() ? `${name}-edited` : "edited", mime = mimeType.split(";")[0]!.trim().toLowerCase();
  return base + (mime === "image/jpeg" ? ".jpg" : mime === "video/mp4" ? ".mp4" : ".bin");
}
