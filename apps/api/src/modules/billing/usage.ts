import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { AccountUnavailable } from "../accounts/repository.js";
import { entitlementResponse, readEntitlements } from "../entitlements/lookup.js";
import { normalizePlan } from "../entitlements/policy.js";
import { personalStorageSummary } from "../storage/summary.js";
import { spaceStorageSummary } from "../storage/space-summary.js";
import { spacePermissions } from "../spaces/permissions.js";
import { refreshPersonalWallet } from "../usage/wallet.js";
import { refreshSpaceWallet } from "../usage/space-wallet.js";
import { BillingUnavailable, type BillingSummaryReader } from "./summary-client.js";
import { billingAiUsage } from "./usage-response.js";

export class BillingUsagePermissionDenied extends Error {}
async function account(tx: PoolClient, userId: string, sessionHash: string) {
  const row = (await tx.query<{ license_id: string }>(`SELECT u.license_id FROM users u JOIN sessions s ON s.user_id=u.id
    WHERE u.id=$1 AND u.lifecycle_state='active' AND s.token_hash=$2 AND s.expires_at>clock_timestamp() FOR SHARE OF u,s`, [userId, sessionHash])).rows[0];
  if (!row) throw new AccountUnavailable();
  return row;
}
interface Space { id: string; name: string; owner_user_id: string; is_default: boolean; updated_at: Date }

export function createBillingUsage(options: { pool: Pool; billing: BillingSummaryReader | null; deployment: "hosted" | "self_hosted" }) {
  return {
    async get(userId: string, sessionHash: string, requestSignal: AbortSignal) {
      const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(25000)]);
      try {
        signal.throwIfAborted();
        const before = await withTransaction(options.pool, async (tx) => {
          await tx.query("SET LOCAL lock_timeout='2s'");
          await tx.query("SET LOCAL statement_timeout='5s'");
          signal.throwIfAborted();
          return account(tx, userId, sessionHash);
        }, { mode: "service" });
        if (options.deployment !== "hosted" || !options.billing) throw new BillingUnavailable();
        // No application locks are held while querying the private billing summary.
        const history = await options.billing({ version: 1, userId, licenseId: before.license_id }, signal);
        return await withTransaction(options.pool, async (tx) => {
          signal.throwIfAborted();
          await tx.query("SET LOCAL lock_timeout='2s'");
          await tx.query("SET LOCAL statement_timeout='5s'");
          const now = (await tx.query<{ now: Date }>("SELECT transaction_timestamp() AS now")).rows[0]!.now;
          // Shared lock order with native usage: all affected Spaces, sorted
          // accounts (including expired reservation owners), then wallets. Never
          // retain a caller account lock while acquiring a later Space lock.
          const spaces = (await tx.query<Space>(`SELECT s.id,s.name,s.owner_user_id,s.is_default,s.updated_at FROM spaces s
            WHERE s.lifecycle_state='active' AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=s.id AND m.user_id=$1)
            ORDER BY s.id FOR UPDATE OF s`, [userId])).rows;
          signal.throwIfAborted();
          const spaceIds = spaces.map((space) => space.id);
          const stale = (await tx.query<{ user_id: string }>(`SELECT DISTINCT user_id FROM hosted_ai_reservations
            WHERE space_id=ANY($1::text[]) AND status='reserved' AND COALESCE(lease_expires_at,created_at+INTERVAL '15 minutes')<=$2`, [spaceIds, now])).rows;
          const accounts = [...new Set([userId, ...spaces.map((space) => space.owner_user_id), ...stale.map((row) => row.user_id)])].sort();
          await tx.query("SELECT id FROM users WHERE id=ANY($1::text[]) ORDER BY id FOR UPDATE", [accounts]);
          const current = await account(tx, userId, sessionHash);
          if (current.license_id !== before.license_id) throw new BillingUnavailable();
          const license = (await tx.query<{ tier: string; status: string; expires_at: Date | null; legacy_tier: string | null }>(
            "SELECT tier,status,expires_at,legacy_tier FROM licenses WHERE id=$1 AND user_id=$2 FOR UPDATE", [current.license_id, userId])).rows[0];
          if (!license) throw new AccountUnavailable();
          const members = (await tx.query<{ space_id: string; role: string }>("SELECT space_id,role FROM space_members WHERE user_id=$1 AND space_id=ANY($2::text[]) FOR SHARE", [userId, spaceIds])).rows;
          const roles = new Map(members.map((member) => [member.space_id, member.role]));
          const visible = spaces.filter((space) => roles.has(space.id));
          for (const space of visible) {
            signal.throwIfAborted();
            const permissions: Record<string, boolean> = await spacePermissions(tx, userId, { ...space, role: roles.get(space.id)! });
            if (!permissions["storage.view_own_usage"]) throw new BillingUsagePermissionDenied();
          }
          if (license.status === "trialing" && license.expires_at && license.expires_at <= now) {
            license.tier = normalizePlan(license.legacy_tier); license.status = "active"; license.expires_at = null;
            await tx.query("UPDATE licenses SET tier=$2,status='active',expires_at=NULL,updated_at=$3 WHERE id=$1", [current.license_id, license.tier, now]);
          }
          // Match Go/native storage advisory names, with one global sorted order.
          for (const key of [`storage-personal:${userId}`, ...visible.map((space) => `storage-space:${space.id}`)].sort()) {
            signal.throwIfAborted(); await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [key]);
          }
          const storage = await personalStorageSummary(tx, userId);
          const entitlements = entitlementResponse(await readEntitlements(tx, userId));
          const rows = [];
          for (const space of visible) {
            signal.throwIfAborted();
            const wallet = await refreshSpaceWallet(tx, { spaceId: space.id, ownerId: space.owner_user_id, now });
            rows.push({ space_id: space.id, name: space.name, role: roles.get(space.id)!, owner_user_id: space.owner_user_id,
              storage: await spaceStorageSummary(tx, userId, space.id, space.owner_user_id, storage.personal), ai: billingAiUsage(wallet), updatedAt: space.updated_at });
          }
          // Space reclamation can release this account's reservations too. Read
          // personal balances last, after every returned Space wallet is refreshed.
          const personal = billingAiUsage(await refreshPersonalWallet(tx, { userId, tier: license.tier, now, ledgerKey: `billing-usage:${userId}:${now.toISOString()}` }));
          signal.throwIfAborted(); await account(tx, userId, sessionHash); signal.throwIfAborted();
          const plan = normalizePlan(license.tier);
          const subscription = history.subscription;
          return { plan, storage, entitlements, personal: { storage: storage.personal, ai: personal },
            spaces: rows.sort((a, b) => b.updatedAt.getTime() - a.updatedAt.getTime() || a.space_id.localeCompare(b.space_id)).map(({ updatedAt: _updatedAt, ...row }) => row),
            agent_usage: { percentage_used: personal.used_ratio * 100, available: personal.available, paused: personal.paused, reset_at: personal.reset_at, plan },
            hosted_ai: { used_ratio: personal.used_ratio, reset_at: personal.reset_at },
            ...(subscription ? { subscription: { status: subscription.status, current_period_end: subscription.currentPeriodEnd,
              cancel_at_period_end: subscription.cancelAtPeriodEnd, billing_interval: subscription.interval } } : {}),
            ...(license.status === "trialing" ? { trial: { status: license.status, ends_at: license.expires_at } } : {}),
          };
        }, { mode: "service" });
      } catch (error: unknown) {
        if (signal.aborted || error && typeof error === "object" && "code" in error && ["55P03", "57014", "40P01"].includes(String(error.code))) throw new BillingUnavailable();
        throw error;
      }
    },
  };
}
