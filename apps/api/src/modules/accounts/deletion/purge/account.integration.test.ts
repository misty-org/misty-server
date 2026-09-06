import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../../../packages/database/src/transaction.js";
import { createAccountDeletionJobs } from "../jobs.js";
import { createDeletionAccountPurgeRepository, privateAccountTables } from "./account.js";

const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], domains: string[] = [];
let application: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_account_purge_test') THEN CREATE ROLE misty_account_purge_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_account_purge_test;
    GRANT SELECT,DELETE ON ${privateAccountTables.join(',')} TO misty_account_purge_test;
    GRANT SELECT,UPDATE ON users,spaces,account_deletion_requests,account_deletion_steps TO misty_account_purge_test;
    GRANT SELECT ON space_members,space_conversations,space_conversation_members,space_action_suggestion_batches,
      ai_invocations,space_integrations,provider_shared_resources,space_library_items,library_files,library_blobs,space_nodes,
      space_notes,space_note_permissions,space_member_roles,space_member_permission_overrides,space_roles,security_domains TO misty_account_purge_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_account_purge_test", max: 3 });
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async tx => {
    await tx.query("DELETE FROM account_deletion_requests WHERE user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = 0; spaces.length = 0; domains.length = 0;
});
afterAll(async () => { if(application) await application.end(); await admin.end(); });
async function user() {
  const id = `state_${randomUUID().replaceAll('-','').slice(0,12)}`; users.push(id);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [id,`${id}@example.invalid`,`license_${id}`]);
    await tx.query("INSERT INTO licenses(id,user_id) VALUES($1,$2)", [`license_${id}`,id]);
    await tx.query("INSERT INTO ai_recaps(user_id,surface_id,prompt,last_result,last_error) VALUES($1,'home','private prompt','private result','private error')", [id]);
    await tx.query("INSERT INTO realtime_tickets(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 day')", [randomUUID(),id]);
    await tx.query("INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 day')", [randomUUID(),id]);
  }); return id;
}
async function fixture() {
  const userId = await user(), other = await user(), space = randomUUID(), domain = randomUUID(), requestId = `deletion_${randomUUID()}`; spaces.push(space); domains.push(domain);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain,other,space]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Preserved Space',$3)", [space,other,domain]);
    await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner')", [space,other]);
    for(const id of [userId,other]) await tx.query("INSERT INTO user_home_activity(user_id,space_id,activity_date) VALUES($1,$2,current_date)", [id,space]);
    await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [userId]);
    await tx.query("INSERT INTO account_deletion_requests(id,user_id,status_token_hash,status,purge_after,cleanup_owner) VALUES($1,$2,$3,'scheduled',now()-interval '1 day','native')", [requestId,userId,randomUUID()]);
    await tx.query("INSERT INTO account_deletion_steps(request_id,step) SELECT $1,unnest(ARRAY['payments','providers','local','purge'])", [requestId]);
    await tx.query("UPDATE account_deletion_steps SET state='completed',completed_at=now() WHERE request_id=$1 AND step<>'purge'", [requestId]);
  });
  const jobs = createAccountDeletionJobs(application), repo = createDeletionAccountPurgeRepository(application);
  const claim = async () => { const job = await jobs.claim('purge'); expect(job).not.toBeNull(); return job!; };
  return {userId,other,space,requestId,jobs,repo,claim};
}
it("erases private recap/history/credentials, preserving another account, Space and financial identity", async () => {
  const f = await fixture();
  const license = (await admin.query("SELECT * FROM licenses WHERE user_id=$1", [f.userId])).rows;
  expect(await f.repo.purgeAccountState(await f.claim())).toBe(true);
  for(const table of ['ai_recaps','user_home_activity','realtime_tickets','sessions']) {
    expect((await admin.query(`SELECT 1 FROM ${table} WHERE user_id=$1`, [f.userId])).rowCount).toBe(0);
    expect((await admin.query(`SELECT 1 FROM ${table} WHERE user_id=$1`, [f.other])).rowCount).toBe(1);
  }
  expect((await admin.query("SELECT * FROM licenses WHERE user_id=$1", [f.userId])).rows).toEqual(license);
  expect((await admin.query("SELECT lifecycle_state FROM spaces WHERE id=$1", [f.space])).rows[0].lifecycle_state).toBe('active');
  expect((await admin.query("SELECT status FROM account_deletion_requests WHERE id=$1", [f.requestId])).rows[0].status).toBe('scheduled');
  expect((await admin.query("SELECT state,completed_at,result FROM account_deletion_steps WHERE request_id=$1 AND step='purge'", [f.requestId])).rows[0]).toEqual({state:'pending',completed_at:null,result:{account_private_state:'purged'}});
  await expect(application.query("DELETE FROM licenses WHERE user_id=$1", [f.userId])).rejects.toMatchObject({code:'42501'});
  await expect(application.query("SELECT * FROM billing.accounts")).rejects.toMatchObject({code:'42501'});
});
it("refuses unfinished recap work and preserves its execution evidence", async () => {
  const f = await fixture(), job = await f.claim();
  await admin.query("UPDATE ai_recaps SET state='running',lease_until=now()+interval '1 minute' WHERE user_id=$1", [f.userId]);
  await expect(f.repo.purgeAccountState(job)).rejects.toThrow('has not stopped');
  expect((await admin.query("SELECT last_result FROM ai_recaps WHERE user_id=$1", [f.userId])).rows[0].last_result).toBe('private result');
  await admin.query("UPDATE ai_recaps SET state='idle',lease_until=NULL WHERE user_id=$1", [f.userId]);
  expect(await f.repo.purgeAccountState(job)).toBe(true);
});
it("enforces retention, prerequisite, account identity and native ownership at effect time", async () => {
  const f = await fixture(), job = await f.claim();
  expect(await f.repo.purgeAccountState({...job,license_id:'wrong'})).toBe(false);
  expect(await f.repo.purgeAccountState({...job,step:'local'})).toBe(false);
  await admin.query("UPDATE account_deletion_requests SET purge_after=now()+interval '1 day' WHERE id=$1", [f.requestId]);
  expect(await f.repo.purgeAccountState(job)).toBe(false);
  await admin.query("UPDATE account_deletion_requests SET purge_after=now()-interval '1 day',cleanup_owner='go' WHERE id=$1", [f.requestId]);
  expect(await f.repo.purgeAccountState(job)).toBe(false);
  await admin.query("UPDATE account_deletion_requests SET cleanup_owner='native' WHERE id=$1", [f.requestId]);
  await admin.query("UPDATE account_deletion_steps SET state='pending',completed_at=NULL WHERE request_id=$1 AND step='local'", [f.requestId]);
  expect(await f.repo.purgeAccountState(job)).toBe(false);
  expect((await admin.query("SELECT 1 FROM ai_recaps WHERE user_id=$1", [f.userId])).rowCount).toBe(1);
});
it("rolls back all erasure on actual lease expiry and preserves earlier phase receipts", async () => {
  const f = await fixture(), job = await f.claim();
  await admin.query("UPDATE account_deletion_steps SET result='{\"agent_data\":\"purged\"}' WHERE request_id=$1 AND step='purge'", [f.requestId]);
  await admin.query(`CREATE FUNCTION misty_test_account_purge_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.user_id='${f.userId}' THEN PERFORM pg_sleep(0.35); END IF; RETURN OLD; END $$;
    CREATE TRIGGER misty_test_account_purge_delay BEFORE DELETE ON user_home_activity FOR EACH ROW EXECUTE FUNCTION misty_test_account_purge_delay()`);
  try {
    await admin.query("UPDATE account_deletion_steps SET lease_expires_at=clock_timestamp()+interval '0.2 seconds' WHERE request_id=$1 AND step='purge'", [f.requestId]);
    await expect(f.repo.purgeAccountState(job)).rejects.toThrow('lease expired');
    expect((await admin.query("SELECT 1 FROM ai_recaps WHERE user_id=$1", [f.userId])).rowCount).toBe(1);
  } finally { await admin.query("DROP TRIGGER misty_test_account_purge_delay ON user_home_activity; DROP FUNCTION misty_test_account_purge_delay()"); }
  const replacement = await f.claim(); expect(await f.repo.purgeAccountState(job)).toBe(false);
  expect(await f.repo.purgeAccountState(replacement)).toBe(true);
  expect((await admin.query("SELECT result FROM account_deletion_steps WHERE request_id=$1 AND step='purge'", [f.requestId])).rows[0].result).toEqual({agent_data:'purged',account_private_state:'purged'});
});
it("rolls back under Space contention and succeeds once the lock is released", async () => {
  const f = await fixture(), job = await f.claim(), blocker = await admin.connect();
  try {
    await blocker.query('BEGIN'); await blocker.query("SELECT id FROM spaces WHERE id=$1 FOR UPDATE", [f.space]);
    await expect(f.repo.purgeAccountState(job)).rejects.toMatchObject({code:'55P03'});
  } finally { await blocker.query('ROLLBACK'); blocker.release(); }
  expect((await admin.query("SELECT 1 FROM ai_recaps WHERE user_id=$1", [f.userId])).rowCount).toBe(1);
  expect(await f.repo.purgeAccountState(job)).toBe(true);
});
