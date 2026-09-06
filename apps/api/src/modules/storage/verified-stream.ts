import { createHash } from "node:crypto";
import { StorageUnavailable, type ObjectMetadata } from "./object-store.js";

/** Preserve backpressure and release the open object on EOF, failure or cancellation. */
export function verifiedObjectStream(source: ReadableStream<Uint8Array>, metadata: ObjectMetadata, signal: AbortSignal, release: () => void) {
  const reader = source.getReader(), hash = createHash("sha256");
  let bytes = 0, finished = false, controller: ReadableStreamDefaultController<Uint8Array>;
  async function close(reason?: unknown) {
    if (finished) return;
    finished = true; signal.removeEventListener("abort", abort);
    try { await reader.cancel(reason).catch(() => {}); } finally { reader.releaseLock(); release(); }
  }
  function abort() { if (!finished) { controller.error(new StorageUnavailable()); void close(signal.reason); } }
  return new ReadableStream<Uint8Array>({
    start(value) { controller = value; signal.addEventListener("abort", abort, { once: true }); if (signal.aborted) abort(); },
    async pull(value) {
      try {
        const next = await reader.read();
        if (finished) return;
        signal.throwIfAborted();
        if (next.done) {
          if (bytes !== metadata.byteSize || hash.digest("hex") !== metadata.sha256) throw new StorageUnavailable();
          value.close(); await close(); return;
        }
        bytes += next.value.byteLength;
        if (bytes > metadata.byteSize) throw new StorageUnavailable();
        hash.update(next.value);
        if (bytes === metadata.byteSize) {
          // With Content-Length, a client can consider the response complete as
          // soon as the final bytes arrive. Verify before releasing that chunk.
          const end = await reader.read();
          if (finished) return;
          signal.throwIfAborted();
          if (!end.done || hash.digest("hex") !== metadata.sha256) throw new StorageUnavailable();
          value.enqueue(next.value); value.close(); await close(); return;
        }
        value.enqueue(next.value);
      } catch (error) { if (!finished) { value.error(new StorageUnavailable()); await close(error); } }
    },
    cancel: close,
  }, { highWaterMark: 0 });
}
