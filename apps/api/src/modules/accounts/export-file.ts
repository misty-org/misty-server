import { mkdtemp, open, unlink, rmdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

export class AccountExportUnavailable extends Error {
  constructor(readonly code = "account_export_unavailable") { super(code); }
}
/** Build the complete manifest before sending headers without retaining it in
 * heap memory. The private file is unlinked immediately: closing the descriptor,
 * including process termination, releases its contents on supported Unix hosts.
 */
export async function createExportFile(options: { signal: AbortSignal; onClose: () => void; maxBytes?: number; readTimeoutMs?: number }) {
  const directory = await mkdtemp(join(tmpdir(), "misty-account-export-"));
  const path = join(directory, "manifest.json");
  let file;
  try { file = await open(path, "wx+", 0o600); await unlink(path); }
  catch (error) { await file?.close(); await unlink(path).catch(() => {}); throw error; }
  finally { await rmdir(directory).catch(() => {}); }
  const handle = file;
  let size = 0, sealed = false, closePromise: Promise<void> | undefined, timer: NodeJS.Timeout | undefined;
  let controller: ReadableStreamDefaultController<Uint8Array> | undefined;
  const close = () => closePromise ??= (async () => {
    clearTimeout(timer); options.signal.removeEventListener("abort", abort);
    try { await handle.close(); } finally { options.onClose(); }
  })();
  const abort = () => { controller?.error(new AccountExportUnavailable()); void close().catch(() => {}); };
  options.signal.addEventListener("abort", abort, { once: true });
  if (options.signal.aborted) { await close(); throw new AccountExportUnavailable(); }
  return {
    close,
    async write(value: string) {
      if (sealed || closePromise || options.signal.aborted) throw new AccountExportUnavailable();
      const bytes = Buffer.from(value);
      if (size + bytes.length > (options.maxBytes ?? 512 * 1024 * 1024)) throw new AccountExportUnavailable("account_export_too_large");
      for (let offset = 0; offset < bytes.length;) {
        options.signal.throwIfAborted();
        const { bytesWritten } = await handle.write(bytes, offset, bytes.length - offset, size);
        if (!bytesWritten) throw new AccountExportUnavailable();
        offset += bytesWritten; size += bytesWritten;
      }
    },
    response() {
      if (sealed || closePromise || options.signal.aborted) throw new AccountExportUnavailable();
      sealed = true; let position = 0;
      const stream = new ReadableStream<Uint8Array>({
        start(value) { controller = value; },
        async pull(value) {
          try {
            if (closePromise) throw new AccountExportUnavailable();
            const bytes = Buffer.allocUnsafe(Math.min(64 * 1024, size - position));
            if (!bytes.length) { await close(); value.close(); return; }
            const read = await handle.read(bytes, 0, bytes.length, position);
            if (!read.bytesRead) throw new AccountExportUnavailable();
            position += read.bytesRead; value.enqueue(bytes.subarray(0, read.bytesRead));
            if (position === size) { await close(); value.close(); }
          } catch { value.error(new AccountExportUnavailable()); await close(); }
        },
        cancel: close,
      });
      timer = setTimeout(abort, options.readTimeoutMs ?? 5 * 60 * 1000); timer.unref();
      return new Response(stream, { headers: { "Content-Type": "application/json; charset=UTF-8", "Cache-Control": "no-store",
        "Content-Length": String(size), "X-Content-Type-Options": "nosniff" } });
    },
  };
}
export type ExportFile = Awaited<ReturnType<typeof createExportFile>>;
