import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { createAccountDeletionJobs } from "./jobs.js";
import { createDeletionLocalRepository, createDeletionLocalWorker } from "./local-step.js";

const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], domains: string[] = [];
let application: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_deletion_local_test') THEN CREATE ROLE misty_deletion_local_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_deletion_local_test;
    GRANT SELECT,UPDATE ON users,spaces,account_deletion_requests,account_deletion_steps,space_runs,agent_run_jobs,
      agent_run_tool_approvals,agent_run_contexts,workflow_device_node_jobs,space_agents,space_workflows,space_notes,space_drawings,user_app_installations TO misty_deletion_local_test;
    GRANT SELECT ON personal_agents,space_integrations,space_provider_credentials,space_conversations,provider_shared_resources,ai_invocations,ai_invocation_contexts TO misty_deletion_local_test;
    GRANT SELECT,DELETE ON space_members,space_conversation_members,app_runtime_sessions TO misty_deletion_local_test;
    GRANT SELECT,INSERT ON space_events,space_note_control_outbox,space_drawing_control_outbox,app_install_events TO misty_deletion_local_test;
    GRANT SELECT,INSERT,UPDATE ON app_data_deletion_jobs TO misty_deletion_local_test;
    GRANT SELECT,INSERT,UPDATE ON owner_storage_usage,space_storage_usage TO misty_deletion_local_test;
    GRANT USAGE ON SEQUENCE space_events_id_seq,app_install_events_id_seq TO misty_deletion_local_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_deletion_local_test", max: 4 });
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async tx => {
    await tx.query("DELETE FROM account_deletion_requests WHERE user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("UPDATE users SET avatar_version=0,avatar_object_key=NULL WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM object_deletion_jobs WHERE created_by_user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = 0; spaces.length = 0; domains.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function user() {
  const id = `local_${randomUUID().replaceAll("-", "").slice(0, 12)}`; users.push(id);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [id, `${id}@example.invalid`, `license_${id}`]);
    await tx.query("INSERT INTO licenses(id,user_id) VALUES($1,$2)", [`license_${id}`, id]);
  }); return id;
}
async function space(owner: string, member?: string) {
  const id = randomUUID(), domain = randomUUID(); spaces.push(id); domains.push(domain);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, owner, id]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Local cleanup',$3)", [id, owner, domain]);
    await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner')", [id, owner]);
    if (member) await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [id, member]);
  }); return id;
}
async function fixture(ready = true) {
  const userId = await user(), requestId = `deletion_${randomUUID()}`;
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [userId]);
  await admin.query("INSERT INTO account_deletion_requests(id,user_id,status_token_hash,purge_after,cleanup_owner) VALUES($1,$2,$3,now()+interval '30 days','native')", [requestId, userId, randomUUID()]);
  await admin.query("INSERT INTO account_deletion_steps(request_id,step) SELECT $1,unnest(ARRAY['payments','providers','local','purge'])", [requestId]);
  const prerequisites = () => admin.query("UPDATE account_deletion_steps SET state='completed',completed_at=now() WHERE request_id=$1 AND step IN ('payments','providers')", [requestId]);
  if (ready) await prerequisites();
  const jobs = createAccountDeletionJobs(application), repository = createDeletionLocalRepository(application);
  const claim = async () => { const job = await jobs.claim("local"); expect(job).not.toBeNull(); return job!; };
  const stage = async () => (await admin.query("SELECT state,result,attempts,last_error_code FROM account_deletion_steps WHERE request_id=$1 AND step='local'", [requestId])).rows[0];
  return { userId, requestId, jobs, repository, claim, stage, prerequisites };
}
it("atomically removes membership, preserves shared notes, schedules default Space and private cleanup, and gates retention", async () => {
  const f = await fixture(), other = await user(), shared = await space(other, f.userId), own = await space(f.userId);
  await admin.query("UPDATE spaces SET is_default=true WHERE id=$1", [own]);
  const note = `note_${randomUUID()}`;
  await admin.query("INSERT INTO space_notes(id,space_id,creator_user_id,title_projection) VALUES($1,$2,$3,'Shared content')", [note, shared, f.userId]);
  await admin.query("UPDATE users SET avatar_version=1 WHERE id=$1", [f.userId]);
  await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version) VALUES($1,'journal','1.1.0'),($2,'journal','1.1.0')", [f.userId, other]);
  const before = (await admin.query("SELECT purge_after FROM account_deletion_requests WHERE id=$1", [f.requestId])).rows[0].purge_after;
  expect(await f.repository.complete(await f.claim())).toBe(true);
  expect(await f.stage()).toMatchObject({ state: "completed", result: { outcome: "local_cleanup_scheduled" } });
  expect((await admin.query("SELECT status,purge_after FROM account_deletion_requests WHERE id=$1", [f.requestId])).rows[0]).toEqual({ status: "scheduled", purge_after: before });
  expect((await admin.query("SELECT user_id FROM space_members WHERE space_id=$1", [shared])).rows).toEqual([{ user_id: other }]);
  expect((await admin.query("SELECT title_projection,lifecycle_state,acl_version FROM space_notes WHERE id=$1", [note])).rows[0]).toMatchObject({ title_projection: "Shared content", lifecycle_state: "active", acl_version: "2" });
  expect((await admin.query("SELECT command FROM space_note_control_outbox WHERE note_id=$1", [note])).rows).toEqual([{ command: "acl" }]);
  expect((await admin.query("SELECT lifecycle_state,permanent_delete_after FROM spaces WHERE id=$1", [own])).rows[0]).toEqual({ lifecycle_state: "pending_deletion", permanent_delete_after: before });
  expect((await admin.query("SELECT avatar_version FROM users WHERE id=$1", [f.userId])).rows[0].avatar_version).toBe("0");
  expect((await admin.query("SELECT object_key FROM object_deletion_jobs WHERE created_by_user_id=$1", [f.userId])).rows).toEqual([{ object_key: `avatars/${f.userId}` }]);
  expect((await admin.query("SELECT state,delete_at FROM app_data_deletion_jobs WHERE user_id=$1", [f.userId])).rows[0]).toEqual({ state: "pending", delete_at: before });
  expect((await admin.query("SELECT state FROM user_app_installations WHERE user_id=$1", [other])).rows[0].state).toBe("installed");
  expect(await f.jobs.claim("purge")).toBeNull(); expect(await createDeletionLocalWorker(application).runOnce()).toBe(false);
  await expect(application.query("SELECT * FROM billing.accounts")).rejects.toMatchObject({ code: "42501" });
});
it("requires both completed prerequisites at claim and at acknowledgement", async () => {
  const f = await fixture(false); expect(await f.jobs.claim("local")).toBeNull();
  await f.prerequisites(); const job = await f.claim();
  await admin.query("UPDATE account_deletion_steps SET state='pending',completed_at=NULL WHERE request_id=$1 AND step='providers'", [f.requestId]);
  expect(await f.repository.complete(job)).toBe(false); expect((await f.stage()).state).toBe("processing");
});
it("rejects stale, expired, wrong-stage and wrong-account claims without advancing cleanup", async () => {
  const f = await fixture(), job = await f.claim();
  expect(await f.repository.complete({ ...job, step: "providers" })).toBe(false);
  expect(await f.repository.complete({ ...job, license_id: "wrong" })).toBe(false);
  expect(await f.repository.complete({ ...job, lease_token: randomUUID() })).toBe(false);
  await admin.query("UPDATE account_deletion_steps SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE request_id=$1 AND step='local'", [f.requestId]);
  expect(await f.repository.complete(job)).toBe(false);
  const replacement = await f.claim(); expect(await f.repository.complete(job)).toBe(false);
  expect(await f.repository.complete(replacement)).toBe(true); expect(await f.repository.complete(replacement)).toBe(false);
});
it("rolls back all effects when a lease expires during SQL execution", async () => {
  const f = await fixture(), own = await space(f.userId), job = await f.claim();
  await admin.query(`CREATE FUNCTION misty_test_local_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
    IF NEW.id='${f.userId}' THEN PERFORM pg_sleep(0.35); END IF; RETURN NEW; END $$;
    CREATE TRIGGER misty_test_local_delay BEFORE UPDATE OF avatar_version ON users FOR EACH ROW EXECUTE FUNCTION misty_test_local_delay()`);
  try {
    await admin.query("UPDATE account_deletion_steps SET lease_expires_at=clock_timestamp()+interval '0.2 seconds' WHERE request_id=$1 AND step='local'", [f.requestId]);
    await expect(f.repository.complete(job)).rejects.toThrow("lease expired");
    expect((await admin.query("SELECT lifecycle_state FROM spaces WHERE id=$1", [own])).rows[0].lifecycle_state).toBe("active");
    expect((await admin.query("SELECT id FROM space_events WHERE space_id=$1", [own])).rowCount).toBe(0);
    expect((await f.stage()).state).toBe("processing");
  } finally { await admin.query("DROP TRIGGER misty_test_local_delay ON users; DROP FUNCTION misty_test_local_delay()"); }
});
it("rechecks active shared ownership and leaves a retryable diagnostic without touching other members", async () => {
  const f = await fixture(), other = await user(), own = await space(f.userId, other);
  expect(await createDeletionLocalWorker(application).runOnce()).toBe(true);
  expect(await f.stage()).toMatchObject({ state: "pending", last_error_code: "local_cleanup_unavailable" });
  expect((await admin.query("SELECT lifecycle_state FROM spaces WHERE id=$1", [own])).rows[0].lifecycle_state).toBe("active");
  expect((await admin.query("SELECT user_id FROM space_members WHERE space_id=$1", [own])).rowCount).toBe(2);
});
it("preserves later Space retention, deleted Spaces and existing App purge attempts", async () => {
  const f = await fixture(), pending = await space(f.userId), deleted = await space(f.userId);
  await admin.query("UPDATE spaces SET lifecycle_state='pending_deletion',permanent_delete_after=now()+interval '40 days' WHERE id=$1", [pending]);
  await admin.query("UPDATE spaces SET lifecycle_state='deleted' WHERE id=$1", [deleted]);
  await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version,state,uninstalled_at,data_deletion_at) VALUES($1,'journal','1.1.0','purging',now()-interval '31 days',now()-interval '1 day')", [f.userId]);
  await admin.query("INSERT INTO app_data_deletion_jobs(user_id,app_id,delete_at,state,attempts,started_at) SELECT user_id,app_id,data_deletion_at,'running',3,now() FROM user_app_installations WHERE user_id=$1", [f.userId]);
  const before = (await admin.query("SELECT * FROM app_data_deletion_jobs WHERE user_id=$1", [f.userId])).rows;
  expect(await f.repository.complete(await f.claim())).toBe(true);
  expect((await admin.query("SELECT permanent_delete_after>now()+interval '39 days' AS preserved FROM spaces WHERE id=$1", [pending])).rows[0].preserved).toBe(true);
  expect((await admin.query("SELECT lifecycle_state FROM spaces WHERE id=$1", [deleted])).rows[0].lifecycle_state).toBe("deleted");
  expect((await admin.query("SELECT * FROM app_data_deletion_jobs WHERE user_id=$1", [f.userId])).rows).toEqual(before);
});
it("does not adopt legacy requests or finish cleanup for a reactivated account", async () => {
  for (const field of ["owner", "account"]) {
    const f = await fixture(), job = await f.claim();
    if (field === "owner") await admin.query("UPDATE account_deletion_requests SET cleanup_owner='go' WHERE id=$1", [f.requestId]);
    else await admin.query("UPDATE users SET lifecycle_state='active' WHERE id=$1", [f.userId]);
    expect(await f.repository.complete(job)).toBe(false); expect((await f.stage()).state).toBe("processing");
  }
});
it("keeps default-Space protection outside an eligible service cleanup lease and always forbids transfer", async () => {
  const f = await fixture(false), own = await space(f.userId), other = await user();
  await admin.query("UPDATE spaces SET is_default=true WHERE id=$1", [own]);
  const update = (sql: string, values: string[] = [own]) => withTransaction(application, tx => tx.query(sql, values), { mode: "service" });
  await expect(update("UPDATE spaces SET lifecycle_state='pending_deletion' WHERE id=$1")).rejects.toMatchObject({ code: "23514" });
  await f.prerequisites(); const job = await f.claim();
  await expect(withTransaction(admin, tx => tx.query("UPDATE spaces SET lifecycle_state='pending_deletion' WHERE id=$1", [own]), { mode: "anonymous", email: "" })).rejects.toMatchObject({ code: "23514" });
  await expect(update("UPDATE spaces SET is_default=false WHERE id=$1")).rejects.toMatchObject({ code: "23514" });
  await expect(update("UPDATE spaces SET owner_user_id=$2 WHERE id=$1", [own, other])).rejects.toMatchObject({ code: "23514" });
  await admin.query("UPDATE users SET lifecycle_state='active' WHERE id=$1", [f.userId]);
  try { await expect(update("UPDATE spaces SET lifecycle_state='pending_deletion' WHERE id=$1")).rejects.toMatchObject({ code: "23514" }); }
  finally { await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.userId]); }
  expect(await f.repository.complete(job)).toBe(true);
});
it("rolls back on a real Space lock timeout and retries cleanly after contention clears", async () => {
  const f = await fixture(), own = await space(f.userId), blocker = await admin.connect();
  try {
    await blocker.query("BEGIN"); await blocker.query("SELECT id FROM spaces WHERE id=$1 FOR UPDATE", [own]);
    expect(await createDeletionLocalWorker(application).runOnce()).toBe(true);
    expect(await f.stage()).toMatchObject({ state: "pending", last_error_code: "local_cleanup_unavailable" });
  } finally { await blocker.query("ROLLBACK"); blocker.release(); }
  expect((await admin.query("SELECT lifecycle_state FROM spaces WHERE id=$1", [own])).rows[0].lifecycle_state).toBe("active");
  await admin.query("UPDATE account_deletion_steps SET available_at=clock_timestamp() WHERE request_id=$1 AND step='local'", [f.requestId]);
  expect(await createDeletionLocalWorker(application).runOnce()).toBe(true);
  expect(await f.stage()).toMatchObject({ state: "completed", attempts: 2, last_error_code: "" });
});
it("queues immutable avatar erasure and rejects a canceled operation without publishing partial cleanup", async () => {
  const f = await fixture(), key = `avatars/avatar_${randomUUID()}`;
  await admin.query("UPDATE users SET avatar_version=1,avatar_object_key=$2 WHERE id=$1", [f.userId, key]);
  const job = await f.claim(), controller = new AbortController(); controller.abort();
  await expect(f.repository.complete(job, controller.signal)).rejects.toThrow();
  expect((await admin.query("SELECT avatar_object_key FROM users WHERE id=$1", [f.userId])).rows[0].avatar_object_key).toBe(key);
  expect(await f.repository.complete(job)).toBe(true);
  expect((await admin.query("SELECT object_key FROM object_deletion_jobs WHERE created_by_user_id=$1", [f.userId])).rows).toEqual([{ object_key: key }]);
});
