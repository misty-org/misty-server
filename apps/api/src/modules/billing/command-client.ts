import { readFile } from "node:fs/promises";
import { importPKCS8, type CryptoKey } from "jose";
import { z } from "zod";
import type { Environment, RuntimeConfig } from "../../../../../packages/runtime/src/config.js";
import { signServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { billingCommandResultSchema, type CheckoutIntent, type BillingSummaryIntent } from "../../../../../packages/service-contracts/src/payments.js";
import { BillingUnavailable } from "./summary-client.js";

const errorCode = z.enum(["billing_account_closed", "subscription_exists", "checkout_in_progress", "checkout_recovery_required", "checkout_identity_mismatch", "checkout_unavailable", "portal_unavailable"]);
export class BillingCommandConflict extends Error { constructor(readonly code: z.infer<typeof errorCode>) { super(code); } }

export interface BillingCommands {
  checkout: (intent: CheckoutIntent, signal: AbortSignal) => Promise<{ url: string }>;
  portal: (intent: BillingSummaryIntent, signal: AbortSignal) => Promise<{ url: string }>;
}
export async function loadBillingCommandClient(env: Environment, runtime: Pick<RuntimeConfig, "deployment" | "environment">): Promise<BillingCommands | null> {
  const endpoint = env.PAYMENTS_COMMANDS_URL?.trim();
  if (!endpoint) return null; // Signing keys may already serve the independent summary client.
  const file = env.API_SIGNING_KEY_FILE?.trim(), keyId = env.API_SIGNING_KEY_ID?.trim();
  if (runtime.deployment !== "hosted" || !file || !keyId || !/^[A-Za-z0-9_-]{1,100}$/.test(keyId)) throw new Error("Invalid private billing command configuration");
  return createBillingCommandClient({ endpoint, keyId, privateKey: await importPKCS8(await readFile(file, "utf8"), "EdDSA"), allowInsecureLoopback: runtime.environment !== "production" });
}
export function createBillingCommandClient(options: { endpoint: string; keyId: string; privateKey: CryptoKey; allowInsecureLoopback?: boolean; fetch?: typeof fetch }): BillingCommands {
  const base = new URL(options.endpoint);
  const local = options.allowInsecureLoopback && base.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(base.hostname);
  if (base.protocol !== "https:" && !local || base.username || base.password || base.search || base.hash || base.pathname !== "/internal/billing") throw new Error("Invalid private billing command endpoint");
  const send = options.fetch ?? globalThis.fetch; let active = 0;
  const command = async (action: "checkout" | "portal", intent: CheckoutIntent | BillingSummaryIntent, requestSignal: AbortSignal) => {
    if (active >= 2) throw new BillingUnavailable();
    active++;
    const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(25000)]);
    try {
      signal.throwIfAborted();
      const endpoint = new URL(`${base.pathname}/${action}`, base), body = Buffer.from(JSON.stringify(intent));
      const token = await signServiceAssertion({ privateKey: options.privateKey, keyId: options.keyId, issuer: "misty-api", audience: "misty-payments",
        subject: intent.userId, scope: `billing:${action}`, request: { method: "POST", path: endpoint.pathname, body } });
      const response = await send(endpoint, { method: "POST", body, redirect: "error", signal,
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" } });
      if (![200, 409].includes(response.status) || !response.body) { await response.body?.cancel(); throw new BillingUnavailable(); }
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
        if (declared && Number(declared) !== size) throw new BillingUnavailable();
        const parsed: unknown = JSON.parse(new TextDecoder("utf8", { fatal: true }).decode(Buffer.concat(chunks)));
        if (response.status === 409) throw new BillingCommandConflict(z.object({ code: errorCode }).strict().parse(parsed).code);
        const result = billingCommandResultSchema.parse(parsed);
        if (result.userId !== intent.userId || result.licenseId !== intent.licenseId) throw new BillingUnavailable();
        return { url: result.url };
      } finally { signal.removeEventListener("abort", cancel); await reader.cancel().catch(() => {}); reader.releaseLock(); }
    } catch (error) { if (error instanceof BillingCommandConflict) throw error; throw new BillingUnavailable(); }
    finally { active--; }
  };
  return { checkout: (intent, signal) => command("checkout", intent, signal), portal: (intent, signal) => command("portal", intent, signal) };
}
