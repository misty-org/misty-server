import { clientInteger } from "../spaces/model.js";
import { available, type UsageWallet } from "../usage/wallet.js";

export function billingAiUsage(wallet: UsageWallet) {
  // Retain the public balance-derived used figure and clamped ratio; the native
  // consumption counter independently prevents refills through plan downgrades.
  const used = wallet.allowance > wallet.remaining ? wallet.allowance - wallet.remaining : 0n;
  const remaining = available(wallet);
  const ratio = wallet.allowance <= 0n || used >= wallet.allowance ? 1 : Number(used) / Number(wallet.allowance);
  return { used: clientInteger(used), reserved: clientInteger(wallet.reserved), limit: clientInteger(wallet.allowance), remaining: clientInteger(remaining),
    used_ratio: ratio, available: remaining > 0n, paused: remaining === 0n, reset_at: wallet.resetAt };
}
