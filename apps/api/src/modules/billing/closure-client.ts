import type { CryptoKey } from "jose";
import { signServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { billingClosureIntentSchema, billingClosureResultSchema, type BillingClosureIntent, type BillingClosureResult } from "../../../../../packages/service-contracts/src/payments.js";

export class BillingClosureUnavailable extends Error {}
export interface BillingClosureClient { close: (intent: BillingClosureIntent, signal: AbortSignal) => Promise<BillingClosureResult> }

/** Private lifecycle client. Construction alone never enables public deletion. */
export function createBillingClosureClient(options: { endpoint: string; keyId: string; privateKey: CryptoKey; allowInsecureLoopback?: boolean; fetch?: typeof fetch }): BillingClosureClient {
  const base = new URL(options.endpoint);
  const local = options.allowInsecureLoopback && base.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(base.hostname);
  if (base.protocol !== "https:" && !local || base.username || base.password || base.search || base.hash || base.pathname !== "/internal/billing") throw new Error("Invalid private billing closure endpoint");
  const endpoint = new URL(`${base.pathname}/account-closure`, base), send = options.fetch ?? globalThis.fetch;
  let active = 0;
  return { async close(input, requestSignal) {
    if (active >= 2) throw new BillingClosureUnavailable();
    active++;
    const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(10000)]);
    try {
      signal.throwIfAborted();
      const intent = billingClosureIntentSchema.parse(input), body = Buffer.from(JSON.stringify(intent));
      const token = await signServiceAssertion({ privateKey: options.privateKey, keyId: options.keyId, issuer: "misty-api", audience: "misty-payments",
        subject: intent.userId, scope: "billing:account-closure", request: { method: "POST", path: endpoint.pathname, body } });
      signal.throwIfAborted();
      const response = await send(endpoint, { method: "POST", body, redirect: "error", signal,
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" } });
      if (response.status !== 200 || !response.body) { await response.body?.cancel(); throw new BillingClosureUnavailable(); }
      const reader = response.body.getReader(), chunks: Uint8Array[] = []; let size = 0;
      const cancel = () => { void reader.cancel().catch(() => {}); };
      signal.addEventListener("abort", cancel, { once: true });
      try {
        signal.throwIfAborted();
        const declared = response.headers.get("Content-Length");
        if (declared && (!/^\d+$/.test(declared) || Number(declared) > 4096)) throw new BillingClosureUnavailable();
        for (;;) { const next = await reader.read(); signal.throwIfAborted(); if (next.done) break;
          size += next.value.byteLength; if (size > 4096) throw new BillingClosureUnavailable(); chunks.push(next.value); }
        if (declared && Number(declared) !== size) throw new BillingClosureUnavailable();
        const result = billingClosureResultSchema.parse(JSON.parse(new TextDecoder("utf8", { fatal: true }).decode(Buffer.concat(chunks))));
        if (result.userId !== intent.userId || result.licenseId !== intent.licenseId || result.deletionRequestId !== intent.deletionRequestId) throw new BillingClosureUnavailable();
        return result;
      } finally { signal.removeEventListener("abort", cancel); await reader.cancel().catch(() => {}); reader.releaseLock(); }
    } catch { throw new BillingClosureUnavailable(); }
    finally { active--; }
  } };
}
