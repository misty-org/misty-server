import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createWebhookRepository, type ClaimedWebhook } from "./repository.js";
import { assertRuntimeDatabaseRole } from "../../../../../packages/database/src/roles.js";

const admin = createTestDatabase();
const fixtureIds: string[] = [];
let payments: Pool;
const newEvent = () => {
  const id = `evt_test_${randomUUID()}`;
  fixtureIds.push(id);
  return { id, type: "test.event", created: 1, payload: { id }, payloadSha256: "a".repeat(64) };
};
function required(job: ClaimedWebhook | null): ClaimedWebhook {
  if (!job) throw new Error("Expected a claimed webhook");
  return job;
}

beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  const migrations = await readMigrations(fileURLToPath(new URL("../../../migrations/", import.meta.url)));
  await applyMigrations(admin, migrations, "billing");
  expect(await applyMigrations(admin, migrations, "billing")).toEqual([]);
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN
      CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_hono_payments_test') THEN
      CREATE ROLE misty_hono_payments_test NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
  END $$;
  GRANT USAGE ON SCHEMA billing TO misty_hono_payments_test; GRANT SELECT ON billing.account_closures TO misty_hono_payments_test;
  GRANT SELECT,INSERT,UPDATE ON billing.webhook_inbox TO misty_hono_payments_test;
  GRANT SELECT,INSERT,UPDATE ON billing.accounts TO misty_hono_payments_test;`);
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_payments_test", max: 4 });
}, 60000);
afterAll(async () => {
  if (payments) await payments.end();
  await admin.query("DELETE FROM billing.webhook_inbox WHERE event_id=ANY($1::text[])", [fixtureIds]);
  await admin.query("DELETE FROM billing.accounts WHERE user_id=ANY($1::text[])", [fixtureIds]);
  await admin.end();
});

describe("durable webhook inbox", () => {
  it("deduplicates concurrent deliveries and only lets one worker claim an event", async () => {
    const inbox = createWebhookRepository(payments);
    const event = newEvent();
    await Promise.all(Array.from({ length: 8 }, () => inbox.accept(event)));
    const claims = await Promise.all(Array.from({ length: 4 }, () => inbox.claim()));
    expect(claims.filter(Boolean)).toHaveLength(1);
    const job = required(claims.find(Boolean) ?? null);
    expect(job.eventId).toBe(event.id);
    expect(await inbox.complete(job, async () => {})).toBe(true);
    await inbox.accept(event);
    expect(await inbox.claim()).toBeNull();
  });

  it("recovers an expired worker lease and fences off its delayed commit", async () => {
    const inbox = createWebhookRepository(payments);
    const event = newEvent();
    await inbox.accept(event);
    const crashedJob = required(await inbox.claim());
    await admin.query("UPDATE billing.webhook_inbox SET lease_expires_at=now()-INTERVAL '1 second' WHERE event_id=$1", [event.id]);
    const recoveredJob = required(await inbox.claim());
    expect(recoveredJob.leaseId).not.toBe(crashedJob.leaseId);
    expect(recoveredJob.attempts).toBe(2);
    expect(await inbox.complete(crashedJob, async () => { throw new Error("Stale worker must not run"); })).toBe(false);
    await inbox.retry(crashedJob, "stripe_unavailable");
    expect(await inbox.complete(recoveredJob, async () => {})).toBe(true);
  });

  it("rolls back domain changes and completion together, then retries with backoff", async () => {
    const inbox = createWebhookRepository(payments);
    const event = newEvent();
    await inbox.accept(event);
    const job = required(await inbox.claim());
    await expect(inbox.complete(job, async (tx) => {
      await tx.query("INSERT INTO billing.accounts(user_id,license_id) VALUES($1,'test')", [event.id]);
      throw new Error("Simulated crash before commit");
    })).rejects.toThrow("Simulated crash");
    expect((await admin.query("SELECT 1 FROM billing.accounts WHERE user_id=$1", [event.id])).rowCount).toBe(0);
    await inbox.retry(job, "processing_failed");
    expect(await inbox.claim()).toBeNull();
    await admin.query("UPDATE billing.webhook_inbox SET available_at=now()-INTERVAL '1 second' WHERE event_id=$1", [event.id]);
    const retried = required(await inbox.claim());
    expect(retried.attempts).toBe(2);
    expect(await inbox.complete(retried, async (tx) => {
      await tx.query("INSERT INTO billing.accounts(user_id,license_id) VALUES($1,'test')", [event.id]);
    })).toBe(true);
  });

  it("cannot read application data or change its migration history", async () => {
    await expect(assertRuntimeDatabaseRole(payments, "payments")).resolves.toBeUndefined();
    await expect(assertRuntimeDatabaseRole(admin, "payments")).rejects.toThrow("isolation");
    await expect(payments.query("SELECT id FROM public.users LIMIT 1")).rejects.toMatchObject({ code: "42501" });
    await expect(payments.query("DELETE FROM billing.goose_db_version")).rejects.toMatchObject({ code: "42501" });
    const application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test" });
    try {
      await expect(assertRuntimeDatabaseRole(application, "api")).resolves.toBeUndefined();
      await expect(assertRuntimeDatabaseRole(application, "payments")).rejects.toThrow("isolation");
      await expect(application.query("SELECT * FROM billing.webhook_inbox LIMIT 1")).rejects.toMatchObject({ code: "42501" });
    } finally { await application.end(); }
  });
});
