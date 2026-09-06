import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { AccountUnavailable } from "../accounts/repository.js";
import { BillingUnavailable } from "./summary-client.js";
import type { BillingCommands } from "./command-client.js";

/** Two admitted operations at the HTTP boundary bound the pool cost of holding
 * account/license/session locks through the single, deadline-limited command.
 * Payments never synchronously calls back into the API in this transaction.
 */
export function createBillingCommands(options: { pool: Pool; client: BillingCommands | null; deployment: "hosted" | "self_hosted" }) {
  const execute = (userId: string, sessionHash: string, signal: AbortSignal, plan?: { tier: "pro" | "max"; interval: "month" | "year" }) => withTransaction(options.pool, async (tx) => {
    signal.throwIfAborted();
    await tx.query("SET LOCAL lock_timeout='2s'");
    const row = (await tx.query<{ license_id: string; email: string; trial_started_at: Date | null; legacy_tier: string | null }>(`SELECT u.license_id,u.email,l.trial_started_at,l.legacy_tier
      FROM users u JOIN licenses l ON l.id=u.license_id AND l.user_id=u.id
      JOIN sessions s ON s.user_id=u.id AND s.token_hash=$2 AND s.expires_at>now()
      WHERE u.id=$1 AND u.lifecycle_state='active' FOR UPDATE OF u,l,s`, [userId, sessionHash])).rows[0];
    if (!row || !(await tx.query("SELECT 1 FROM sessions WHERE token_hash=$1 AND expires_at>clock_timestamp()", [sessionHash])).rowCount) throw new AccountUnavailable();
    if (!options.client || options.deployment !== "hosted") throw new BillingUnavailable();
    const identity = { version: 1 as const, userId, licenseId: row.license_id };
    const result = plan ? await options.client.checkout({ ...identity, ...plan, email: row.email,
      trialEligible: plan.tier === "pro" && row.trial_started_at === null && row.legacy_tier === null }, signal) : await options.client.portal(identity, signal);
    signal.throwIfAborted();
    // The locks serialize deletion/logout. Recheck wall-clock expiry after a
    // slow provider call before returning a capability URL to the caller.
    if (!(await tx.query("SELECT 1 FROM sessions WHERE token_hash=$1 AND expires_at>clock_timestamp()", [sessionHash])).rowCount) throw new AccountUnavailable();
    return result;
  }, { mode: "service" }).catch((error: unknown) => {
    if (error && typeof error === "object" && "code" in error && ["55P03", "57014"].includes(String(error.code))) throw new BillingUnavailable();
    throw error;
  });
  return {
    checkout: (userId: string, sessionHash: string, plan: { tier: "pro" | "max"; interval: "month" | "year" }, signal: AbortSignal) => execute(userId, sessionHash, signal, plan),
    portal: (userId: string, sessionHash: string, signal: AbortSignal) => execute(userId, sessionHash, signal),
  };
}
