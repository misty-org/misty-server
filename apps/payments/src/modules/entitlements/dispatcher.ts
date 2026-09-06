import type { CryptoKey } from "jose";
import { z } from "zod";
import { signServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import type { createEntitlementOutbox } from "./repository.js";

const acknowledgementSchema = z.object({
  eventId: z.uuid(), result: z.enum(["applied", "duplicate", "superseded"]),
});

async function readAcknowledgement(response: Response): Promise<unknown> {
  if (!response.body) throw new Error("Missing acknowledgement");
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > 4096) throw new Error("Acknowledgement exceeds size limit");
      chunks.push(value);
    }
    return JSON.parse(Buffer.concat(chunks).toString("utf8"));
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
}

export function createEntitlementDispatcher(options: {
  endpoint: string;
  privateKey: CryptoKey;
  keyId: string;
  outbox: ReturnType<typeof createEntitlementOutbox>;
  fetch?: typeof fetch;
  allowInsecureLoopback?: boolean;
}) {
  const endpoint = new URL(options.endpoint);
  const loopbackHttp = options.allowInsecureLoopback && endpoint.protocol === "http:" &&
    ["localhost", "127.0.0.1", "[::1]"].includes(endpoint.hostname);
  if ((endpoint.protocol !== "https:" && !loopbackHttp) || endpoint.username || endpoint.password ||
      endpoint.hash || endpoint.search || endpoint.pathname !== "/internal/payments/entitlements") {
    throw new Error("Entitlements endpoint must be an HTTPS service endpoint");
  }
  const send = options.fetch ?? globalThis.fetch;
  return {
    async runOnce(): Promise<boolean> {
      const delivery = await options.outbox.claim();
      if (!delivery) return false;
      try {
        const body = Buffer.from(JSON.stringify(delivery.event));
        const assertion = await signServiceAssertion({
          privateKey: options.privateKey, keyId: options.keyId,
          issuer: "misty-payments", audience: "misty-api", subject: delivery.event.userId, scope: "entitlements:write",
          request: { method: "POST", path: endpoint.pathname, body },
        });
        const response = await send(endpoint, {
          method: "POST", headers: { Authorization: `Bearer ${assertion}`, "Content-Type": "application/json" },
          body, redirect: "error", signal: AbortSignal.timeout(10000),
        });
        if (response.status !== 200) {
          await response.body?.cancel();
          await options.outbox.retry(delivery, response.status === 409 ? "projection_conflict" : "projection_unavailable");
          return true;
        }
        const ack = acknowledgementSchema.parse(await readAcknowledgement(response));
        if (ack.eventId !== delivery.event.eventId) throw new Error("Acknowledgement identifies another event");
        await options.outbox.acknowledge(delivery);
      } catch {
        // Ambiguous network failures retry the same event ID; the API inbox deduplicates it.
        await options.outbox.retry(delivery, "projection_delivery_failed");
      }
      return true;
    },
  };
}
