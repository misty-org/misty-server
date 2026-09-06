export type ObjectMetadata = { byteSize: number; sha256: string; mimeType: string };
export type UploadTransfer = { url: string; method: "PUT"; headers: Record<string, string>; expires_at: string };
export type DownloadTransfer = { url: string; expires_at: string; filename: string };
export interface ObjectStore {
  head(key: string, signal?: AbortSignal): Promise<ObjectMetadata | null>;
  delete(key: string, signal?: AbortSignal): Promise<void>;
  signUpload(key: string, metadata: ObjectMetadata, expires: Date): Promise<UploadTransfer>;
  signDownload(key: string, filename: string, expires: Date): Promise<DownloadTransfer>;
}
/** Bounded server-mediated transfers for small account assets. */
export interface ByteObjectStore {
  getBytes(key: string, maxBytes: number, signal?: AbortSignal): Promise<Buffer | null>;
  putBytes(key: string, data: Buffer, metadata: ObjectMetadata, signal?: AbortSignal): Promise<void>;
}
export interface StreamingObjectStore {
  openStream(key: string, signal?: AbortSignal): Promise<{ metadata: ObjectMetadata; body: ReadableStream<Uint8Array> } | null>;
}
export class StorageUnavailable extends Error { constructor() { super("Object storage is unavailable"); } }
export function objectKey(key: string) {
  if (!/^(library|avatars)\/[A-Za-z0-9_-]{8,160}$/.test(key)) throw new Error("Invalid object key");
  return key;
}
export function objectMetadata(value: ObjectMetadata) {
  if (!Number.isSafeInteger(value.byteSize) || value.byteSize < 1 || value.byteSize > 1_000_000_000 ||
    !/^[a-f0-9]{64}$/.test(value.sha256) || !/^[\w.+-]+\/[\w.+-]+$/.test(value.mimeType)) throw new Error("Invalid object metadata");
  return value;
}
export function storageFilename(value: string) {
  const name = value.replaceAll("\\", "/").split("/").at(-1)!.replace(/[\x00-\x1f\x7f]/g, "").trim();
  return [...name].slice(0, 255).join("");
}
export function downloadDisposition(filename: string) {
  const safe = storageFilename(filename) || "download", ascii = safe.replace(/[^\x20-\x7e]|["\\]/g, "_");
  const encoded = encodeURIComponent(safe).replace(/['()*]/g, char => `%${char.charCodeAt(0).toString(16).toUpperCase()}`);
  return `attachment; filename="${ascii}"; filename*=UTF-8''${encoded}`;
}
