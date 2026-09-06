import { readFile } from "node:fs/promises";
import { importPKCS8, type CryptoKey } from "jose";
import type { Environment, RuntimeConfig } from "../../../../../packages/runtime/src/config.js";
import { signServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { billingSummarySchema, type BillingSummary, type BillingSummaryIntent } from "../../../../../packages/service-contracts/src/payments.js";

export class BillingUnavailable extends Error {}
export type BillingSummaryReader = (identity: BillingSummaryIntent, signal: AbortSignal) => Promise<BillingSummary>;
export async function loadBillingSummaryClient(env: Environment, runtime: Pick<RuntimeConfig, "deployment" | "environment">): Promise<BillingSummaryReader | null> {
  const endpoint = env.PAYMENTS_SUMMARY_URL?.trim(), file = env.API_SIGNING_KEY_FILE?.trim(), keyId = env.API_SIGNING_KEY_ID?.trim();
  if (!endpoint && env.PAYMENTS_COMMANDS_URL?.trim()) {
    if (runtime.deployment !== "hosted") throw new Error("Invalid private billing client configuration");
    return null; // The command client owns validation of its shared signing keys.
  }
  if (![endpoint, file, keyId].some(Boolean)) return null;
  if (runtime.deployment !== "hosted" || !endpoint || !file || !keyId || !/^[A-Za-z0-9_-]{1,100}$/.test(keyId)) throw new Error("Invalid private billing client configuration");
  return createBillingSummaryClient({ endpoint, keyId, privateKey: await importPKCS8(await readFile(file, "utf8"), "EdDSA"), allowInsecureLoopback: runtime.environment !== "production" });
}
export function createBillingSummaryClient(options: { endpoint: string; keyId: string; privateKey: CryptoKey; allowInsecureLoopback?: boolean; fetch?: typeof fetch }): BillingSummaryReader {
  const endpoint = new URL(options.endpoint);
  const local = options.allowInsecureLoopback && endpoint.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(endpoint.hostname);
  if (endpoint.protocol !== "https:" && !local || endpoint.username || endpoint.password || endpoint.search || endpoint.hash || endpoint.pathname !== "/internal/billing/summary") throw new Error("Invalid private billing summary endpoint");
  const send = options.fetch ?? globalThis.fetch;
  let active = 0;
  return async (identity, requestSignal) => {
    if (active >= 16) throw new BillingUnavailable();
    active++;
    const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(5000)]);
    try {
      signal.throwIfAborted();
      const body = Buffer.from(JSON.stringify(identity));
      const token = await signServiceAssertion({ privateKey: options.privateKey, keyId: options.keyId,
        issuer: "misty-api", audience: "misty-payments", subject: identity.userId, scope: "billing:summary",
        request: { method: "POST", path: endpoint.pathname, body } });
      const response = await send(endpoint, { method: "POST", body, redirect: "error", signal,
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" } });
      if (response.status !== 200 || !response.body) { await response.body?.cancel(); throw new BillingUnavailable(); }
      const reader = response.body.getReader(), chunks: Uint8Array[] = []; let size = 0;
      const cancel = () => { void reader.cancel().catch(() => {}); };
      signal.addEventListener("abort", cancel, { once: true });
      try {
        signal.throwIfAborted();
        const declared = response.headers.get("Content-Length");
        if (declared && (!/^\d+$/.test(declared) || Number(declared) > 8192)) throw new BillingUnavailable();
        for (;;) {
          const next = await reader.read(); signal.throwIfAborted(); if (next.done) break;
          size += next.value.byteLength; if (size > 8192) throw new BillingUnavailable(); chunks.push(next.value);
        }
        if (declared && size !== Number(declared)) throw new BillingUnavailable();
        const result = billingSummarySchema.parse(JSON.parse(new TextDecoder("utf8", { fatal: true }).decode(Buffer.concat(chunks))));
        if (result.userId !== identity.userId || result.licenseId !== identity.licenseId) throw new BillingUnavailable();
        return result;
      } finally { signal.removeEventListener("abort", cancel); await reader.cancel().catch(() => {}); reader.releaseLock(); }
    } catch { throw new BillingUnavailable(); }
    finally { active--; }
  };
}
