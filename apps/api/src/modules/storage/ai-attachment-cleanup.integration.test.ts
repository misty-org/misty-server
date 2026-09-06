import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createStorageJobs } from "./jobs.js";

const admin = createTestDatabase(), users: string[] = [], keys: string[] = [];
let application: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_ai_attachment_cleanup_test') THEN CREATE ROLE misty_ai_attachment_cleanup_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_ai_attachment_cleanup_test;
    GRANT SELECT,UPDATE,DELETE ON object_deletion_jobs TO misty_ai_attachment_cleanup_test;
    GRANT SELECT,UPDATE,DELETE ON ai_conversation_attachments,agent_conversations TO misty_ai_attachment_cleanup_test;
    GRANT SELECT ON users,space_library_uploads,library_blobs,library_files,library_legal_holds,space_note_assets,space_drawing_assets,
      space_notes,space_drawings,spaces,space_members,space_conversations,space_conversation_members TO misty_ai_attachment_cleanup_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_ai_attachment_cleanup_test", max: 3 });
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async tx => {
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM object_deletion_jobs WHERE object_key=ANY($1::text[])", [keys]);
  }); users.length = 0; keys.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture() {
  const userId = `attach_${randomUUID().replaceAll("-", "").slice(0, 12)}`, conversationId = `conversation_${randomUUID()}`; users.push(userId);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [userId, `${userId}@example.invalid`, `license_${userId}`]);
    await tx.query("INSERT INTO licenses(id,user_id) VALUES($1,$2)", [`license_${userId}`, userId]);
    await tx.query("INSERT INTO agent_conversations(id,user_id) VALUES($1,$2)", [conversationId, userId]);
  });
  const add = async (original = `library/${randomUUID()}_original`, model = `library/${randomUUID()}_model`) => {
    const id = `attachment_${randomUUID()}`; keys.push(original, model);
    await admin.query(`INSERT INTO ai_conversation_attachments(id,user_id,conversation_id,scope,display_name,mime_type,byte_size,sha256,width,height,
      object_key,model_mime_type,model_byte_size,model_sha256,model_width,model_height,model_object_key,lifecycle_state)
      VALUES($1,$2,$3,'conversation','private.png','image/png',64,repeat('a',64),1,1,$4,'image/png',32,repeat('b',64),1,1,$5,'ready')`, [id, userId, conversationId, original, model]);
    return { id, original, model };
  };
  const tx = <T>(work: Parameters<typeof withTransaction<T>>[1]) => withTransaction(application, work, { mode: "service" });
  const queued = async () => (await admin.query("SELECT object_key,created_by_user_id,not_before,attempts,lease_id FROM object_deletion_jobs WHERE object_key=ANY($1::text[]) ORDER BY object_key", [keys])).rows;
  const due = () => admin.query("UPDATE object_deletion_jobs SET not_before=clock_timestamp()-interval '1 second' WHERE object_key=ANY($1::text[])", [keys]);
  return { userId, conversationId, add, tx, queued, due };
}
it("retains both image cleanup intents through a conversation cascade without granting the deleting role queue insertion", async () => {
  const f = await fixture(), attachment = await f.add();
  await f.tx(tx => tx.query("DELETE FROM agent_conversations WHERE id=$1", [f.conversationId]));
  const rows = await f.queued(); expect(rows.map(r => r.object_key).sort()).toEqual([attachment.original, attachment.model].sort());
  for (const row of rows) { expect(row.created_by_user_id).toBe(f.userId); expect(row.not_before.getTime()).toBeGreaterThan(Date.now()+29*60000); }
  await expect(application.query("INSERT INTO object_deletion_jobs(object_key,not_before) VALUES('library/forbidden',now())")).rejects.toMatchObject({ code: "42501" });
});
it("rolls back attachment deletion and its cleanup intents together", async () => {
  const f = await fixture(), attachment = await f.add();
  await expect(f.tx(async tx => { await tx.query("DELETE FROM ai_conversation_attachments WHERE id=$1", [attachment.id]); throw new Error("rollback fixture"); })).rejects.toThrow("rollback fixture");
  expect(await f.queued()).toEqual([]);
  expect((await admin.query("SELECT id FROM ai_conversation_attachments WHERE id=$1", [attachment.id])).rowCount).toBe(1);
});
it("queues replaced keys and soft deletion, preserving current variants and a later cleanup deadline", async () => {
  const f = await fixture(), attachment = await f.add(), replacement = `library/${randomUUID()}_replacement`; keys.push(replacement);
  await f.tx(tx => tx.query("UPDATE ai_conversation_attachments SET object_key=$2 WHERE id=$1", [attachment.id, replacement]));
  expect((await f.queued()).map(r => r.object_key)).toEqual([attachment.original]);
  await admin.query("UPDATE object_deletion_jobs SET not_before=now()+interval '2 hours' WHERE object_key=$1", [attachment.original]);
  await f.tx(tx => tx.query("UPDATE ai_conversation_attachments SET lifecycle_state='deleted' WHERE id=$1", [attachment.id]));
  const rows = await f.queued(); expect(rows.map(r => r.object_key).sort()).toEqual([attachment.original, replacement, attachment.model].sort());
  expect(rows.find(r => r.object_key === attachment.original)!.not_before.getTime()).toBeGreaterThan(Date.now()+119*60000);
});
it("protects a key referenced by another live attachment and retries a failed remote deletion", async () => {
  const f = await fixture(), first = await f.add(), second = await f.add(undefined, first.original);
  await f.tx(tx => tx.query("DELETE FROM ai_conversation_attachments WHERE id=$1", [first.id])); await f.due();
  let fail = true;
  const remove = vi.fn(async (_key: string) => { if (fail) throw new Error("storage unavailable"); });
  const worker = createStorageJobs(application, { delete: remove });
  await worker.runOnce(); expect(remove.mock.calls.flat()).toEqual([first.model]);
  expect(await f.queued()).toHaveLength(2);
  fail = false; await f.due(); await worker.runOnce();
  expect((await f.queued()).map(r => r.object_key)).toEqual([first.original]);
  await f.tx(tx => tx.query("DELETE FROM ai_conversation_attachments WHERE id=$1", [second.id])); await f.due(); await worker.runOnce();
  expect(await f.queued()).toEqual([]); expect(remove).toHaveBeenCalledWith(first.original);
});
it("keeps cleanup intents after the account itself is deleted and deduplicates identical image variants", async () => {
  const f = await fixture(), key = `library/${randomUUID()}_same`; await f.add(key, key);
  await admin.query("DELETE FROM users WHERE id=$1", [f.userId]);
  expect(await f.queued()).toEqual([expect.objectContaining({ object_key: key, created_by_user_id: null })]);
});
