import { createHash } from "node:crypto";

export class UsageConflict extends Error {}
export class UsageForbidden extends Error {}
export class UsageNotFound extends Error {}
export class UsageLimitReached extends Error {
  constructor(readonly required: bigint, readonly available: bigint, readonly scope: "personal" | "space") { super("Hosted AI weekly limit reached"); }
}
export type Reservation = { id: string; userId: string; spaceId: string | null; amount: bigint; generation: number; status: string };
export type Usage = { provider: string; model: string; inputTokens: bigint; cachedInputTokens: bigint; outputTokens: bigint;
  reasoningTokens: bigint; providerCost: bigint; chargeMicrousd: bigint };
export const defaultRateCardVersion = "2026-07-22-weekly-microusd-v1";
export function validateAmount(value: bigint, positive = false) {
  if (typeof value !== "bigint" || value < (positive ? 1n : 0n) || value > 9223372036854775807n) throw new Error("Invalid usage amount");
}
export function validateKey(key: string) {
  if (!key.trim() || key.length > 512) throw new Error("Invalid usage idempotency key");
}
export function usageFingerprint(usage: Usage): string {
  for (const amount of [usage.inputTokens, usage.cachedInputTokens, usage.outputTokens, usage.reasoningTokens, usage.providerCost, usage.chargeMicrousd]) validateAmount(amount);
  if (usage.provider.length > 256 || usage.model.length > 512) throw new Error("Invalid usage provider or model");
  return createHash("sha256").update(JSON.stringify([usage.provider, usage.model, usage.inputTokens.toString(), usage.cachedInputTokens.toString(),
    usage.outputTokens.toString(), usage.reasoningTokens.toString(), usage.providerCost.toString(), usage.chargeMicrousd.toString()])).digest("hex");
}
