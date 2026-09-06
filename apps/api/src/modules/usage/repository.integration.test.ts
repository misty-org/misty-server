import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createEntitlementRepository } from "../entitlements/repository.js";
import { createEntitlementEffects } from "../entitlements/service.js";
import { createUsageRepository } from "./repository.js";
import { UsageLimitReached, type Usage } from "./model.js";

const admin = createTestDatabase();
let application: Pool;
let now = new Date("2026-09-11T12:00:00Z");
const users: string[] = [], spaces: string[] = [];
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
  END $$;
  GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
  GRANT SELECT,UPDATE ON users,licenses,spaces TO misty_hono_app_test;
  GRANT SELECT ON space_members TO misty_hono_app_test;
  GRANT SELECT,INSERT,UPDATE ON hosted_ai_wallets,space_hosted_ai_wallets,hosted_ai_reservations,hosted_ai_usage_ledger,
    payment_entitlement_inbox,payment_entitlement_projections TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 8 });
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async (tx) => {
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM security_domains WHERE owner_user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM payment_entitlement_inbox WHERE user_id=ANY($1::text[])", [users]);
  });
  users.length = 0; spaces.length = 0;
  now = new Date("2026-09-11T12:00:00Z");
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
const repository = () => createUsageRepository({ pool: application, now: () => now });
const usage = (chargeMicrousd: bigint): Usage => ({ provider: "test", model: "model", inputTokens: 10n, cachedInputTokens: 0n,
  outputTokens: 20n, reasoningTokens: 0n, providerCost: chargeMicrousd, chargeMicrousd });
async function account(tier = "basic") {
  const userId = `usage_${randomUUID().replaceAll("-", "").slice(0, 12)}`, licenseId = `license_${userId}`;
  users.push(userId);
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [userId, `${userId}@example.invalid`, licenseId]);
    await tx.query("INSERT INTO licenses(id,user_id,tier) VALUES($1,$2,$3)", [licenseId, userId, tier]);
  });
  let revision = 0;
  return { userId, licenseId,
    reserve: (amount: bigint, spaceId?: string, key: string = randomUUID(), allowPartial = false) => repository().reserve({ userId,
      ...(spaceId ? { spaceId } : {}), meter: "assistant_ai", idempotencyKey: key, amount, allowPartial }),
    wallet: async () => (await admin.query("SELECT weekly_remaining_microusd,weekly_consumed_microusd,reserved_microusd FROM hosted_ai_wallets WHERE user_id=$1", [userId])).rows[0],
    changePlan: (tier: "basic" | "pro" | "max") => createEntitlementRepository({ pool: application, ...createEntitlementEffects({ now: () => now }) }).receive({
      version: 1, eventId: randomUUID(), userId, licenseId, generatedAt: now.toISOString(), revision: String(++revision),
      subscription: { subscriptionId: `sub_${userId}`, tier: tier === "basic" ? "pro" : tier, interval: "month", status: tier === "basic" ? "canceled" : "active",
        currentPeriodEnd: "2026-10-11T12:00:00Z", cancelAtPeriodEnd: false },
    }),
  };
}
async function space(owner: string, members: string[]) {
  const id = `space_${randomUUID()}`, domain = `domain_${id}`;
  spaces.push(id);
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, owner, id]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Usage test',$3)", [id, owner, domain]);
    for (const member of new Set([owner, ...members])) await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,$3)", [id, member, member === owner ? "owner" : "member"]);
  });
  return id;
}

it("serializes concurrent personal reservations without overspending", async () => {
  const user = await account();
  const results = await Promise.allSettled(Array.from({ length: 5 }, () => user.reserve(40_000n)));
  expect(results.filter((result) => result.status === "fulfilled")).toHaveLength(3);
  for (const result of results) if (result.status === "rejected") expect(result.reason).toBeInstanceOf(UsageLimitReached);
  expect(await user.wallet()).toMatchObject({ reserved_microusd: "120000", weekly_remaining_microusd: "150000" });
});

it("charges the initiating member and Space while preserving the owner's personal allowance", async () => {
  const owner = await account("pro"), member = await account();
  const id = await space(owner.userId, [member.userId]);
  const { reservation } = await member.reserve(20_000n, id);
  const wallet = await repository().settle(reservation, "member-settle", usage(7_000n));
  expect(wallet).toMatchObject({ remaining: 143_000n, consumed: 7_000n, reserved: 0n });
  expect((await admin.query("SELECT weekly_remaining_microusd,weekly_consumed_microusd FROM space_hosted_ai_wallets WHERE space_id=$1", [id])).rows[0])
    .toEqual({ weekly_remaining_microusd: "893000", weekly_consumed_microusd: "7000" });
  expect(await owner.wallet()).toBeUndefined();
});

it("shares Space capacity across members and preserves the other member's reserved budget at settlement", async () => {
  const owner = await account(), first = await account("pro"), second = await account("pro");
  const id = await space(owner.userId, [first.userId, second.userId]);
  const a = await first.reserve(90_000n, id), b = await second.reserve(90_000n, id, randomUUID(), true);
  expect(b.reservation.amount).toBe(60_000n);
  await expect(first.reserve(1n, id)).rejects.toMatchObject({ scope: "space", available: 0n });
  await repository().settle(a.reservation, "first-settle", usage(200_000n));
  const remaining = (await admin.query("SELECT weekly_remaining_microusd,reserved_microusd FROM space_hosted_ai_wallets WHERE space_id=$1", [id])).rows[0];
  expect(remaining).toEqual({ weekly_remaining_microusd: "60000", reserved_microusd: "60000" });
  await repository().settle(b.reservation, "second-settle", usage(60_000n));
});

it("binds partial retries to the original request and fences a previous reservation generation", async () => {
  const user = await account();
  const first = await user.reserve(200_000n, undefined, "partial", true);
  expect(first.reservation.amount).toBe(150_000n);
  expect((await user.reserve(200_000n, undefined, "partial", true)).reservation.id).toBe(first.reservation.id);
  await expect(user.reserve(300_000n, undefined, "partial", true)).rejects.toThrow("parameters changed");
  await repository().release(first.reservation);
  const retried = await user.reserve(200_000n, undefined, "partial", true);
  expect(retried.reservation.generation).toBe(first.reservation.generation + 1);
  await expect(repository().settle(first.reservation, "late-settle", usage(20_000n))).rejects.toThrow("generation");
  await expect(repository().release(first.reservation)).rejects.toThrow("generation");
  expect(await user.wallet()).toMatchObject({ reserved_microusd: "150000", weekly_consumed_microusd: "0" });
});

it("retains over-limit consumption through downgrade/reupgrade and refunds it without minting allowance", async () => {
  const user = await account("pro");
  const { reservation } = await user.reserve(800_000n);
  await repository().settle(reservation, "large-settle", usage(800_000n));
  await user.changePlan("basic");
  expect(await user.wallet()).toMatchObject({ weekly_remaining_microusd: "0", weekly_consumed_microusd: "800000" });
  await user.changePlan("pro");
  expect(await user.wallet()).toMatchObject({ weekly_remaining_microusd: "100000", weekly_consumed_microusd: "800000" });
  await user.changePlan("basic");
  await repository().refund(reservation, "large-refund", "internal_policy");
  expect(await user.wallet()).toMatchObject({ weekly_remaining_microusd: "150000", weekly_consumed_microusd: "0" });
  await user.changePlan("pro");
  expect(await user.wallet()).toMatchObject({ weekly_remaining_microusd: "900000", weekly_consumed_microusd: "0" });
});

it("prevents settlement-key collisions and duplicate refunds under a different key", async () => {
  const user = await account();
  const a = await user.reserve(20_000n), b = await user.reserve(10_000n);
  await repository().settle(a.reservation, "shared-settle", usage(20_000n));
  await expect(repository().settle(b.reservation, "shared-settle", usage(10_000n))).rejects.toThrow("different operation");
  await repository().settle(b.reservation, "b-settle", usage(10_000n));
  await repository().refund(a.reservation, "a-refund", "internal_policy");
  await repository().refund(a.reservation, "a-refund-again", "internal_policy");
  expect(await user.wallet()).toMatchObject({ weekly_remaining_microusd: "140000", weekly_consumed_microusd: "10000" });
  expect((await admin.query("SELECT count(*) FROM hosted_ai_usage_ledger WHERE reservation_id=$1 AND source LIKE 'internal_failure_refund%'", [a.reservation.id])).rows[0].count).toBe("1");
});

it("does not credit a previous week's refund against this week's usage", async () => {
  const user = await account();
  const previous = await user.reserve(20_000n);
  await repository().settle(previous.reservation, "last-week", usage(20_000n));
  now = new Date("2026-09-14T12:00:00Z");
  const current = await user.reserve(10_000n);
  await repository().settle(current.reservation, "this-week", usage(10_000n));
  await repository().refund(previous.reservation, "old-refund", "internal_policy");
  expect(await user.wallet()).toMatchObject({ weekly_remaining_microusd: "140000", weekly_consumed_microusd: "10000" });
});

it("reclaims stale Space reservations from both wallets when another member returns", async () => {
  const owner = await account(), member = await account();
  const id = await space(owner.userId, [member.userId]);
  const old = await member.reserve(100_000n, id);
  now = new Date(now.getTime() + 16 * 60_000);
  await owner.reserve(100_000n, id);
  expect(await member.wallet()).toMatchObject({ reserved_microusd: "0" });
  expect((await admin.query("SELECT status FROM hosted_ai_reservations WHERE id=$1", [old.reservation.id])).rows[0].status).toBe("released");
});

it("rejects a nonmember and a reservation handle from another account", async () => {
  const owner = await account(), stranger = await account();
  const id = await space(owner.userId, []);
  await expect(stranger.reserve(1n, id)).rejects.toThrow("membership");
  const own = await owner.reserve(1n);
  await expect(repository().settle({ ...own.reservation, userId: stranger.userId }, "foreign", usage(1n))).rejects.toThrow("unavailable");
});

it("rolls back wallet and reservation changes when the consumption ledger cannot be written", async () => {
  const user = await account();
  const { reservation } = await user.reserve(20_000n);
  await admin.query("REVOKE INSERT ON hosted_ai_usage_ledger FROM misty_hono_app_test");
  try {
    await expect(repository().settle(reservation, "failed-ledger", usage(10_000n))).rejects.toThrow("permission denied");
  } finally { await admin.query("GRANT INSERT ON hosted_ai_usage_ledger TO misty_hono_app_test"); }
  expect(await user.wallet()).toMatchObject({ weekly_remaining_microusd: "150000", reserved_microusd: "20000", weekly_consumed_microusd: "0" });
  expect((await admin.query("SELECT status FROM hosted_ai_reservations WHERE id=$1", [reservation.id])).rows[0].status).toBe("reserved");
});

it("preserves Space consumption when its owner's plan is downgraded and restored", async () => {
  const owner = await account("pro"), member = await account("pro");
  const id = await space(owner.userId, [member.userId]);
  const job = await member.reserve(800_000n, id);
  await repository().settle(job.reservation, "space-large", usage(700_000n));
  await owner.changePlan("basic");
  expect((await repository().wallets({ userId: member.userId, spaceId: id })).space).toMatchObject({ allowance: 150_000n, consumed: 700_000n, remaining: 0n });
  await owner.changePlan("pro");
  expect((await repository().wallets({ userId: member.userId, spaceId: id })).space).toMatchObject({ allowance: 900_000n, consumed: 700_000n, remaining: 200_000n });
});

it("locks overlapping Space cleanup accounts in the same order", async () => {
  const first = await account(), second = await account();
  const a = await space(first.userId, [second.userId]), b = await space(second.userId, [first.userId]);
  await second.reserve(40_000n, a);
  await first.reserve(40_000n, b);
  now = new Date(now.getTime() + 16 * 60_000);
  const results = await Promise.all([first.reserve(40_000n, a), second.reserve(40_000n, b)]);
  expect(results).toHaveLength(2);
  expect(await first.wallet()).toMatchObject({ reserved_microusd: "40000" });
  expect(await second.wallet()).toMatchObject({ reserved_microusd: "40000" });
});

it("renews a live generation but never resurrects an expired reservation", async () => {
  const user = await account();
  const job = await user.reserve(20_000n);
  now = new Date(now.getTime() + 14 * 60_000);
  expect(await repository().renew(job.reservation)).toBe(true);
  expect((await admin.query("SELECT created_at FROM hosted_ai_reservations WHERE id=$1", [job.reservation.id])).rows[0].created_at).toEqual(new Date("2026-09-11T12:00:00Z"));
  now = new Date(now.getTime() + 14 * 60_000);
  expect((await repository().wallets({ userId: user.userId })).personal.reserved).toBe(20_000n);
  now = new Date(now.getTime() + 2 * 60_000);
  expect(await repository().renew(job.reservation)).toBe(false);
  expect((await repository().wallets({ userId: user.userId })).personal.reserved).toBe(0n);
});
