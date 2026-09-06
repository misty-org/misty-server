import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { BillingUnavailable, type BillingSummaryReader } from "../billing/summary-client.js";
import { normalizePlan } from "../entitlements/policy.js";
import { AccountUnavailable } from "./repository.js";

type AccountRow = {
  id: string; license_id: string; name: string; username: string; email: string; avatar_version: string; created_at: Date;
  tier: string; status: string; expires_at: Date | null; trial_started_at: Date | null; legacy_tier: string | null; license_device: string;
};
export function createAccountSummary(options: { pool: Pool; billing: BillingSummaryReader | null; deployment: "hosted" | "self_hosted" }) {
  const account = (userId: string, sessionHash: string) => withTransaction(options.pool, async (tx) => {
    const row = (await tx.query<AccountRow>(`SELECT u.id,u.license_id,u.name,u.username,u.email,u.avatar_version::text,u.created_at,
      l.tier,l.status,l.expires_at,l.trial_started_at,l.legacy_tier,l.license_device
      FROM users u JOIN licenses l ON l.id=u.license_id AND l.user_id=u.id
      JOIN sessions s ON s.user_id=u.id AND s.token_hash=$2 AND s.expires_at>now()
      WHERE u.id=$1 AND u.lifecycle_state='active' FOR SHARE OF u,l,s`, [userId, sessionHash])).rows[0];
    if (!row) throw new AccountUnavailable();
    if (options.deployment === "self_hosted" && !(await tx.query("SELECT user_id FROM self_host_accounts WHERE user_id=$1 AND disabled_at IS NULL AND entitlement_expires_at>now() FOR SHARE", [userId])).rowCount) throw new AccountUnavailable();
    return row;
  }, { mode: "service" });
  return {
    async get(userId: string, sessionHash: string, signal: AbortSignal) {
      const before = await account(userId, sessionHash);
      if (options.deployment === "hosted" && !options.billing) throw new BillingUnavailable();
      const history = options.deployment === "hosted" ? await options.billing!({ version: 1, userId, licenseId: before.license_id }, signal) : null;
      const row = await account(userId, sessionHash); signal.throwIfAborted();
      if (row.license_id !== before.license_id) throw new BillingUnavailable();
      const subscription = history?.subscription ?? null;
      const avatarVersion = Number(row.avatar_version);
      if (!Number.isSafeInteger(avatarVersion) || avatarVersion < 0) throw new Error("Avatar version exceeds the account contract");
      const billingKind = row.status === "trialing" ? "trial" : subscription && ["active", "trialing"].includes(subscription.status) ? "subscription" : row.legacy_tier !== null ? "lifetime" : "free";
      return { id: row.id, name: row.name, username: row.username, email: row.email, avatar_version: avatarVersion, created_at: row.created_at,
        tier: normalizePlan(row.tier), status: row.status, allows_use: ["active", "trialing"].includes(row.status),
        expires_at: row.expires_at, trial_started_at: row.trial_started_at,
        trial_eligible: row.trial_started_at === null && row.legacy_tier === null && !subscription && !history?.hasCompletedPurchase,
        license_device: row.license_device,
        billing: { kind: billingKind, interval: subscription?.interval ?? null, subscription_status: subscription?.status ?? null,
          current_period_end: subscription?.currentPeriodEnd ?? null, cancel_at_period_end: subscription?.cancelAtPeriodEnd ?? false,
          customer_portal_available: subscription?.customerPortalAvailable ?? false },
      };
    },
  };
}
export type AccountSummary = ReturnType<typeof createAccountSummary>;
