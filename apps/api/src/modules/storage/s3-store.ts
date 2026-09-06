import { Agent as HttpAgent } from "node:http";
import { Agent as HttpsAgent } from "node:https";
import { createHash } from "node:crypto";
import { S3Client, PutObjectCommand, GetObjectCommand, HeadObjectCommand, DeleteObjectCommand } from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";
import type { S3Config } from "./s3-config.js";
import { objectKey, objectMetadata, storageFilename, StorageUnavailable, type ObjectStore, type ByteObjectStore } from "./object-store.js";

const checksumMetadata = "misty-library-sha256";
export function createS3Store(config: S3Config, now = () => new Date()): ObjectStore & ByteObjectStore & { close(): void } {
  const client = new S3Client({ endpoint: config.endpoint, region: config.region, forcePathStyle: config.forcePathStyle,
    credentials: { accessKeyId: config.accessKeyId, secretAccessKey: config.secretAccessKey },
    maxAttempts: 2, requestChecksumCalculation: "WHEN_REQUIRED", responseChecksumValidation: "WHEN_REQUIRED",
    requestHandler: { connectionTimeout: 2000, requestTimeout: 8000,
      httpAgent: new HttpAgent({ keepAlive: true, maxSockets: 16 }), httpsAgent: new HttpsAgent({ keepAlive: true, maxSockets: 16 }) } });
  let active = 0;
  async function bounded<T>(signal: AbortSignal | undefined, operation: (signal: AbortSignal) => Promise<T>) {
    if (active >= 16) throw new StorageUnavailable();
    active++;
    try { return await operation(AbortSignal.any([AbortSignal.timeout(10000), ...(signal ? [signal] : [])])); }
    catch (error) {
      if (error && typeof error === "object" && "$metadata" in error && (error.$metadata as { httpStatusCode?: number })?.httpStatusCode === 404) return null;
      throw new StorageUnavailable();
    } finally { active--; }
  }
  function lifetime(expires: Date) {
    const signingDate = now(), expiresIn = Math.floor((expires.getTime() - signingDate.getTime()) / 1000);
    if (expiresIn < 1 || expiresIn > 3600) throw new Error("Invalid signed transfer lifetime");
    return { signingDate, expiresIn };
  }
  return {
    async putBytes(key, data, metadata, signal) {
      objectMetadata(metadata);
      if (data.length > 16 * 1024 * 1024 || data.length !== metadata.byteSize || createHash("sha256").update(data).digest("hex") !== metadata.sha256) throw new StorageUnavailable();
      const response = await bounded(signal, (abortSignal) => client.send(new PutObjectCommand({ Bucket: config.bucket, Key: objectKey(key), Body: data,
        ContentLength: data.length, ContentType: metadata.mimeType, ChecksumSHA256: Buffer.from(metadata.sha256, "hex").toString("base64"),
        Metadata: { [checksumMetadata]: metadata.sha256 } }), { abortSignal }));
      if (!response) throw new StorageUnavailable();
    },
    async getBytes(key, maxBytes, signal) {
      if (!Number.isSafeInteger(maxBytes) || maxBytes < 1 || maxBytes > 16 * 1024 * 1024) throw new StorageUnavailable();
      return bounded(signal, async (abortSignal) => {
        const response = await client.send(new GetObjectCommand({ Bucket: config.bucket, Key: objectKey(key) }), { abortSignal });
        if (!response.Body) throw new StorageUnavailable();
        const reader = response.Body.transformToWebStream().getReader();
        const cancel = () => { void reader.cancel().catch(() => {}); };
        abortSignal.addEventListener("abort", cancel, { once: true });
        try {
          abortSignal.throwIfAborted();
          if (response.ContentLength !== undefined && response.ContentLength > maxBytes) throw new StorageUnavailable();
          const chunks: Uint8Array[] = []; let size = 0;
          for (;;) { const next = await reader.read(); abortSignal.throwIfAborted(); if (next.done) break; size += next.value.byteLength; if (size > maxBytes) throw new StorageUnavailable(); chunks.push(next.value); }
          if (response.ContentLength !== undefined && size !== response.ContentLength) throw new StorageUnavailable();
          const data = Buffer.concat(chunks, size), expected = response.Metadata?.[checksumMetadata];
          if (expected && createHash("sha256").update(data).digest("hex") !== expected.toLowerCase()) throw new StorageUnavailable();
          return data;
        } finally { abortSignal.removeEventListener("abort", cancel); await reader.cancel().catch(() => {}); reader.releaseLock(); }
      });
    },
    async head(key, signal) {
      const response = await bounded(signal, (abortSignal) => client.send(new HeadObjectCommand({ Bucket: config.bucket, Key: objectKey(key) }), { abortSignal }));
      if (!response) return null;
      try { return objectMetadata({ byteSize: response.ContentLength ?? 0, mimeType: response.ContentType ?? "",
        sha256: response.Metadata?.[checksumMetadata]?.trim().toLowerCase() ?? "" }); }
      catch { throw new StorageUnavailable(); }
    },
    async delete(key, signal) {
      await bounded(signal, (abortSignal) => client.send(new DeleteObjectCommand({ Bucket: config.bucket, Key: objectKey(key) }), { abortSignal }));
    },
    async signUpload(key, metadata, expires) {
      objectMetadata(metadata);
      const headers = { "Content-Type": metadata.mimeType, "x-amz-checksum-sha256": Buffer.from(metadata.sha256, "hex").toString("base64"),
        [`x-amz-meta-${checksumMetadata}`]: metadata.sha256 };
      const url = await getSignedUrl(client, new PutObjectCommand({ Bucket: config.bucket, Key: objectKey(key), ContentLength: metadata.byteSize,
        ContentType: metadata.mimeType, ChecksumSHA256: headers["x-amz-checksum-sha256"], Metadata: { [checksumMetadata]: metadata.sha256 } }),
      { ...lifetime(expires), signableHeaders: new Set(["content-type", "content-length"]),
        unhoistableHeaders: new Set(["x-amz-checksum-sha256", `x-amz-meta-${checksumMetadata}`]) });
      // Content-Length is signed, but browsers set it from the bounded Blob body.
      return { url, method: "PUT", headers, expires_at: expires.toISOString() };
    },
    async signDownload(key, filename, expires) {
      const safe = storageFilename(filename) || "download", ascii = safe.replace(/[^\x20-\x7e]|["\\]/g, "_");
      const encoded = encodeURIComponent(safe).replace(/['()*]/g, (char) => `%${char.charCodeAt(0).toString(16).toUpperCase()}`);
      const disposition = `attachment; filename="${ascii}"; filename*=UTF-8''${encoded}`;
      const url = await getSignedUrl(client, new GetObjectCommand({ Bucket: config.bucket, Key: objectKey(key), ResponseContentDisposition: disposition }), lifetime(expires));
      return { url, filename: safe, expires_at: expires.toISOString() };
    },
    close: () => client.destroy(),
  };
}
