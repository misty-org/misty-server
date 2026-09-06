type Budget = { perIdentity: bigint; global: bigint };
export class EgressQuotaExceeded extends Error {}
export function loadEgressBudget(env: NodeJS.ProcessEnv): Budget {
  function bytes(name: string, fallback: bigint) {
    const raw = env[name]?.trim() ?? "";
    if (!/^\+?\d+$/.test(raw)) return fallback;
    const value = BigInt(raw); return value > 0n && value <= 9223372036854775807n ? value : fallback;
  }
  return { perIdentity: bytes("MISTY_EGRESS_MAX_BYTES_PER_IDENTITY_DAY", 25n << 30n), global: bytes("MISTY_EGRESS_MAX_BYTES_PER_DAY", 200n << 30n) };
}
/** Matches Go's process-local rolling day budgets; callers share one instance. */
export function createEgressGuard(budget: Budget, now = Date.now, maxKeys = 20000) {
  type Record = { bytes: bigint; end: number };
  const identities = new Map<string, Record>(); let global: Record = { bytes: 0n, end: 0 };
  return {
    charge(identity: string, size: number) {
      if (!Number.isSafeInteger(size) || size < 0) throw new EgressQuotaExceeded();
      const time = now(), bytes = BigInt(size);
      if (global.end <= time) global = { bytes: 0n, end: time + 86400000 };
      let account = identities.get(identity);
      if (!account || account.end <= time) {
        if (!account && identities.size >= maxKeys) for (const [key, value] of identities) if (value.end <= time) identities.delete(key);
        // Saturation must not silently discard the per-account ceiling.
        if (!account && identities.size >= maxKeys) throw new EgressQuotaExceeded();
        account = { bytes: 0n, end: time + 86400000 };
      }
      // Include this transfer before approving; Go could overshoot by one file.
      if (global.bytes + bytes > budget.global || account.bytes + bytes > budget.perIdentity) throw new EgressQuotaExceeded();
      account.bytes += bytes; global.bytes += bytes; identities.set(identity, account);
    },
  };
}
