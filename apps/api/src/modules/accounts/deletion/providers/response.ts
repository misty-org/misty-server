import { ProviderCleanupError } from "./credentials.js";
export async function readCleanupJson(response: Response, signal: AbortSignal): Promise<unknown> {
  if (!response.body) throw new ProviderCleanupError("provider_cleanup_unavailable");
  const reader = response.body.getReader(), chunks: Uint8Array[] = []; let size = 0;
  const cancel = () => { void reader.cancel().catch(() => {}); }; signal.addEventListener("abort", cancel, { once: true });
  try {
    signal.throwIfAborted();
    const declared = response.headers.get("Content-Length");
    if (declared && (!/^\d+$/.test(declared) || Number(declared) > 16384)) throw new ProviderCleanupError("provider_cleanup_unavailable");
    for (;;) { const next = await reader.read(); signal.throwIfAborted(); if (next.done) break;
      size += next.value.byteLength; if (size > 16384) throw new ProviderCleanupError("provider_cleanup_unavailable"); chunks.push(next.value); }
    if (declared && Number(declared) !== size) throw new ProviderCleanupError("provider_cleanup_unavailable");
    const raw = Buffer.concat(chunks);
    try { return JSON.parse(new TextDecoder("utf8", { fatal: true }).decode(raw)); }
    finally { raw.fill(0); }
  } finally { signal.removeEventListener("abort", cancel); await reader.cancel().catch(() => {}); reader.releaseLock(); for (const chunk of chunks) chunk.fill(0); }
}
