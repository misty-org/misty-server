import { createHash, randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { AppSessionRevoked, createAppRuntimeRepository } from "./repository.js";

const admin = createTestDatabase();
const suffix = randomUUID().replaceAll("-", "").slice(0, 12);
const accountId = `sdk_${suffix}`;
const licenseId = `license_${suffix}`;
const tokenHash = createHash("sha256").update(randomUUID()).digest("hex");
let application: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN
      CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
  END $$;
  GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
  GRANT SELECT, INSERT, UPDATE, DELETE ON users, user_app_installations, app_runtime_sessions, app_personal_records TO misty_hono_app_test;`);
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO users (id, email, password_hash, username, license_id) VALUES ($1,$2,'test-only',$3,$4)", [accountId, `${accountId}@example.invalid`, accountId, licenseId]);
    await tx.query("INSERT INTO licenses (id,user_id) VALUES ($1,$2)", [licenseId, accountId]);
    await tx.query("INSERT INTO user_app_installations (user_id,app_id,installed_version,granted_scopes) VALUES ($1,'notes','0.1.0','[\"storage.read\",\"storage.write\"]')", [accountId]);
    await tx.query("INSERT INTO app_runtime_sessions (token_hash,user_id,app_id,scopes,expires_at) VALUES ($1,$2,'notes','[\"storage.read\",\"storage.write\"]',NOW()+INTERVAL '5 minutes')", [tokenHash, accountId]);
  }, { mode: "service" });
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 2 });
}, 60000);
afterAll(async () => {
  if (application) await application.end();
  await admin.query("DELETE FROM users WHERE id=$1", [accountId]);
  await admin.end();
});

describe("app sessions with a real restricted database role", () => {
  it("stores records only in the authenticated account and app namespace", async () => {
    const repository = createAppRuntimeRepository(application);
    const session = await repository.findSession(tokenHash);
    expect(session?.user_id).toBe(accountId);
    if (!session) throw new Error("Missing fixture session");
    const record = await repository.putRecord(session, "draft", { title: "Test note" });
    expect(record.data).toEqual({ title: "Test note" });
    await expect(repository.listRecords({ ...session, app_id: "another-app" })).rejects.toBeInstanceOf(AppSessionRevoked);
    await expect(repository.listRecords({ ...session, user_id: "another-user" })).rejects.toBeInstanceOf(AppSessionRevoked);
    expect(await repository.listRecords(session)).toHaveLength(1);
    expect(await repository.deleteRecord(session, "draft")).toBe(true);
  });
  it("invalidates an existing session when installation scopes are revoked", async () => {
    const repository = createAppRuntimeRepository(application);
    await admin.query("UPDATE user_app_installations SET granted_scopes='[]' WHERE user_id=$1", [accountId]);
    expect(await repository.findSession(tokenHash)).toBeNull();
  });
});
