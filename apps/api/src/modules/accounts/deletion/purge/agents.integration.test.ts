import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../../../packages/database/src/transaction.js";
import { createAccountDeletionJobs } from "../jobs.js";
import { createDeletionPurgeRepository } from "./repository.js";

const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], domains: string[] = [], objectKeys: string[] = [];
let application: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_agent_purge_test') THEN CREATE ROLE misty_agent_purge_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_agent_purge_test;
    GRANT SELECT,UPDATE ON users,spaces,account_deletion_requests,account_deletion_steps,space_runs,personal_agents,personal_agent_versions,
      agent_run_jobs,agent_run_tool_approvals,personal_agent_mcp_tools,agent_toolbox_action_journal TO misty_agent_purge_test;
    GRANT SELECT,DELETE ON agent_conversations,agent_conversation_events,space_agent_conversations,space_agent_conversation_events,
      ai_conversation_attachments,ai_invocations,ai_artifacts,ai_feedback,ai_retrieval_documents,ai_surface_preferences,ai_cleanup_jobs,
      ai_user_settings,misty_memories,space_agent_instances,agent_run_contexts TO misty_agent_purge_test;
    GRANT SELECT ON space_members,space_conversations,space_conversation_members,space_agents,space_agent_versions,
      provider_shared_resources,provider_content_records,space_integrations,space_notes,space_drawings,workflow_device_node_jobs,
      space_calendar_events,space_calendar_sources,mcp_remote_connections,mcp_remote_tools TO misty_agent_purge_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_agent_purge_test", max: 3 });
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async tx => {
    await tx.query("DELETE FROM account_deletion_requests WHERE user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM personal_agents WHERE owner_user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM object_deletion_jobs WHERE object_key=ANY($1::text[])", [objectKeys]);
  }); users.length = 0; spaces.length = 0; domains.length = 0; objectKeys.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function account() {
  const userId = `purge_${randomUUID().replaceAll("-", "").slice(0, 12)}`, conversation = `conversation_${randomUUID()}`,
    agent = randomUUID(), version = randomUUID(), invocation = randomUUID(), memory = randomUUID(), document = randomUUID(); users.push(userId);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [userId, `${userId}@example.invalid`, `license_${userId}`]);
    await tx.query("INSERT INTO licenses(id,user_id) VALUES($1,$2)", [`license_${userId}`, userId]);
    await tx.query("INSERT INTO personal_agents(id,owner_user_id,name,instructions,voice_id,enabled,model_id) VALUES($1,$2,'Private Agent','secret instruction','private-voice',false,'test-model')", [agent, userId]);
    await tx.query(`INSERT INTO personal_agent_versions(id,agent_id,version,name,instructions,model_mode,model_id,checksum_sha256,created_by_user_id)
      VALUES($1,$2,1,'Private Version','secret version','pinned','model',repeat('a',64),$3)`, [version, agent, userId]);
    await tx.query("INSERT INTO agent_conversations(id,user_id,state,personal_agent_id) VALUES($1,$2,'{\"private\":true}',$3)", [conversation, userId, agent]);
    await tx.query("INSERT INTO agent_conversation_events(conversation_id,user_id,event_type,data) VALUES($1,$2,'user_message','{\"text\":\"private\"}')", [conversation, userId]);
    await tx.query(`INSERT INTO ai_invocations(id,user_id,surface_id,mode,trigger_kind,state,idempotency_key,request_payload)
      VALUES($1,$2,'test','quick','message','completed',$1,'{"private":true}')`, [invocation, userId]);
    await tx.query("INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload) VALUES($1,1,'result','{\"text\":\"private\"}')", [invocation]);
    await tx.query(`INSERT INTO ai_artifacts(id,invocation_id,user_id,schema_version,kind,title,risk,approval_policy,idempotency_key,state,expires_at)
      VALUES($1,$1,$2,1,'text_patch','Private Artifact','draft','none',$1,'applied',now())`, [invocation, userId]);
    await tx.query("INSERT INTO misty_memories(id,user_id,scope_key,memory_key,content,source_invocation_id) VALUES($1,$2,'personal',repeat('b',64),'private memory',$3)", [memory, userId, invocation]);
    await tx.query("INSERT INTO ai_retrieval_documents(id,source_kind,source_id,owner_user_id,privacy_class,source_revision,title,href) VALUES($1,'private',$1,$2,'private','1','private','private')", [document, userId]);
    await tx.query("INSERT INTO ai_retrieval_chunks(document_id,ordinal,content,content_hash) VALUES($1,0,'private chunk','hash')", [document]);
    await tx.query(`INSERT INTO agent_toolbox_action_journal(idempotency_key,user_id,tool_name,audit_event,risk,source,request,result,state,session_id)
      VALUES($1,$2,'tool','write','write','agent','{"secret":"input"}','{"secret":"output"}','completed','private-session')`, [userId, userId]);
  });
  const original = `library/${randomUUID()}_original`, model = `library/${randomUUID()}_model`; objectKeys.push(original, model);
  await admin.query(`INSERT INTO ai_conversation_attachments(id,user_id,conversation_id,scope,display_name,mime_type,byte_size,sha256,width,height,object_key,
    model_mime_type,model_byte_size,model_sha256,model_width,model_height,model_object_key,lifecycle_state)
    VALUES($1,$2,$3,'conversation','private.png','image/png',20,repeat('a',64),1,1,$4,'image/png',10,repeat('b',64),1,1,$5,'ready')`, [invocation, userId, conversation, original, model]);
  return { userId, agent, version, invocation, memory, document, conversation, original, model };
}
async function fixture() {
  const data = await account(), requestId = `deletion_${randomUUID()}`;
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [data.userId]);
  await admin.query("INSERT INTO account_deletion_requests(id,user_id,status_token_hash,status,purge_after,cleanup_owner) VALUES($1,$2,$3,'scheduled',now()-interval '1 second','native')", [requestId, data.userId, randomUUID()]);
  await admin.query("INSERT INTO account_deletion_steps(request_id,step) SELECT $1,unnest(ARRAY['payments','providers','local','purge'])", [requestId]);
  await admin.query("UPDATE account_deletion_steps SET state='completed',completed_at=now() WHERE request_id=$1 AND step<>'purge'", [requestId]);
  const jobs = createAccountDeletionJobs(application), repository = createDeletionPurgeRepository(application);
  const claim = async () => { const job = await jobs.claim('purge'); expect(job).not.toBeNull(); return job!; };
  return { ...data, requestId, jobs, repository, claim };
}
async function run(owner: string, requester: string, agent: string) {
  const space = randomUUID(), domain = randomUUID(), id = randomUUID(); spaces.push(space); domains.push(domain);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, owner, space]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Shared',$3)", [space, owner, domain]);
    await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner')", [space, owner]);
    await tx.query(`INSERT INTO space_runs(id,space_id,resource_kind,resource_id,agent_id,owner_user_id,initiated_by_user_id,billing_user_id,requesting_member_id,
      trigger_kind,state,input,result,agent_version_snapshot,context_bindings) VALUES($1,$2,'agent',$3,$3,$4,$4,$4,$4,'manual','completed','{"private":true}',
      '{"answer":"preserve for other requester"}','{"instructions":"private owner prompt"}','[{"private":"path"}]')`, [id, space, agent, requester]);
  }); return { id, space };
}
it("purges private AI data, queues images and redacts owned definitions while preserving other accounts and accounting identities", async () => {
  const f = await fixture(), other = await account(), mine = await run(other.userId, f.userId, f.agent), shared = await run(other.userId, other.userId, f.agent);
  const license = (await admin.query("SELECT * FROM licenses WHERE user_id=$1", [f.userId])).rows;
  expect(await f.repository.purgeAgents(await f.claim())).toBe(true);
  for (const table of ['agent_conversations','agent_conversation_events','ai_invocations','ai_artifacts','misty_memories','ai_conversation_attachments']) {
    expect((await admin.query(`SELECT 1 FROM ${table} WHERE user_id=$1`, [f.userId])).rowCount).toBe(0);
    expect((await admin.query(`SELECT 1 FROM ${table} WHERE user_id=$1`, [other.userId])).rowCount).toBeGreaterThan(0);
  }
  expect((await admin.query("SELECT 1 FROM ai_invocation_events WHERE invocation_id=$1", [f.invocation])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM ai_retrieval_chunks WHERE document_id=$1", [f.document])).rowCount).toBe(0);
  expect((await admin.query("SELECT name,instructions,voice_id,enabled FROM personal_agents WHERE id=$1", [f.agent])).rows[0]).toEqual({ name:'Deleted Agent', instructions:'',voice_id:'',enabled:false });
  expect((await admin.query("SELECT instructions FROM personal_agent_versions WHERE id=$1", [f.version])).rows[0].instructions).toBe('');
  expect((await admin.query("SELECT input,result,context_bindings FROM space_runs WHERE id=$1", [mine.id])).rows[0]).toEqual({ input:{},result:{redacted:true},context_bindings:[] });
  expect((await admin.query("SELECT result,agent_version_snapshot FROM space_runs WHERE id=$1", [shared.id])).rows[0]).toEqual({ result:{answer:'preserve for other requester'},agent_version_snapshot:{} });
  expect((await admin.query("SELECT request,result,state,session_id FROM agent_toolbox_action_journal WHERE user_id=$1", [f.userId])).rows[0]).toEqual({request:{},result:{redacted:true},state:'completed',session_id:null});
  expect((await admin.query("SELECT object_key FROM object_deletion_jobs WHERE object_key=ANY($1::text[])", [[f.original,f.model]])).rowCount).toBe(2);
  expect((await admin.query("SELECT * FROM licenses WHERE user_id=$1", [f.userId])).rows).toEqual(license);
  expect((await admin.query("SELECT lifecycle_state FROM users WHERE id=$1", [f.userId])).rows[0].lifecycle_state).toBe('pending_deletion');
  expect((await admin.query("SELECT status FROM account_deletion_requests WHERE id=$1", [f.requestId])).rows[0].status).toBe('scheduled');
  expect((await admin.query("SELECT state,completed_at,result FROM account_deletion_steps WHERE request_id=$1 AND step='purge'", [f.requestId])).rows[0]).toEqual({state:'pending',completed_at:null,result:{agent_data:'purged',attachment_objects:'queued'}});
  await expect(application.query("SELECT * FROM billing.accounts")).rejects.toMatchObject({code:'42501'});
});
it("requires retention at both claim and effect time and all prerequisite receipts", async () => {
  const f = await fixture(); await admin.query("UPDATE account_deletion_requests SET purge_after=now()+interval '1 day' WHERE id=$1", [f.requestId]);
  expect(await f.jobs.claim('purge')).toBeNull();
  await admin.query("UPDATE account_deletion_requests SET purge_after=now()-interval '1 day' WHERE id=$1", [f.requestId]); const job = await f.claim();
  await admin.query("UPDATE account_deletion_requests SET purge_after=now()+interval '1 day' WHERE id=$1", [f.requestId]); expect(await f.repository.purgeAgents(job)).toBe(false);
  await admin.query("UPDATE account_deletion_requests SET purge_after=now()-interval '1 day' WHERE id=$1", [f.requestId]);
  await admin.query("UPDATE account_deletion_steps SET state='pending',completed_at=NULL WHERE request_id=$1 AND step='providers'", [f.requestId]); expect(await f.repository.purgeAgents(job)).toBe(false);
  expect((await admin.query("SELECT 1 FROM misty_memories WHERE id=$1", [f.memory])).rowCount).toBe(1);
});
it("rejects wrong identity, wrong stage and an expired or replaced lease", async () => {
  const f = await fixture(), job = await f.claim();
  expect(await f.repository.purgeAgents({...job,step:'local'})).toBe(false);
  expect(await f.repository.purgeAgents({...job,license_id:'wrong'})).toBe(false);
  expect(await f.repository.purgeAgents({...job,lease_token:randomUUID()})).toBe(false);
  await admin.query("UPDATE account_deletion_steps SET lease_expires_at=now()-interval '1 second' WHERE request_id=$1 AND step='purge'", [f.requestId]);
  expect(await f.repository.purgeAgents(job)).toBe(false); const replacement = await f.claim();
  expect(await f.repository.purgeAgents(job)).toBe(false); expect(await f.repository.purgeAgents(replacement)).toBe(true);
  expect(await f.repository.purgeAgents(replacement)).toBe(false);
});
it("rolls back erased rows and image intents if the lease expires during the transaction", async () => {
  const f = await fixture(), job = await f.claim();
  await admin.query(`CREATE FUNCTION misty_test_purge_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.id='${f.memory}' THEN PERFORM pg_sleep(0.35); END IF; RETURN OLD; END $$;
    CREATE TRIGGER misty_test_purge_delay BEFORE DELETE ON misty_memories FOR EACH ROW EXECUTE FUNCTION misty_test_purge_delay()`);
  try {
    await admin.query("UPDATE account_deletion_steps SET lease_expires_at=clock_timestamp()+interval '0.2 seconds' WHERE request_id=$1 AND step='purge'", [f.requestId]);
    await expect(f.repository.purgeAgents(job)).rejects.toThrow('lease expired');
    expect((await admin.query("SELECT 1 FROM agent_conversations WHERE id=$1", [f.conversation])).rowCount).toBe(1);
    expect((await admin.query("SELECT 1 FROM object_deletion_jobs WHERE object_key=$1", [f.original])).rowCount).toBe(0);
    expect((await admin.query("SELECT instructions FROM personal_agents WHERE id=$1", [f.agent])).rows[0].instructions).toBe('secret instruction');
  } finally { await admin.query("DROP TRIGGER misty_test_purge_delay ON misty_memories; DROP FUNCTION misty_test_purge_delay()"); }
});
it("blocks erasure while related agent or AI work is active", async () => {
  const f = await fixture(), job = await f.claim();
  await admin.query("UPDATE ai_invocations SET state='running' WHERE id=$1", [f.invocation]);
  await expect(f.repository.purgeAgents(job)).rejects.toThrow('has not stopped');
  await admin.query("UPDATE ai_invocations SET state='completed' WHERE id=$1", [f.invocation]);
  await admin.query("UPDATE agent_toolbox_action_journal SET state='started' WHERE user_id=$1", [f.userId]);
  await expect(f.repository.purgeAgents(job)).rejects.toThrow('side effect has not stopped');
  await admin.query("UPDATE agent_toolbox_action_journal SET state='completed' WHERE user_id=$1", [f.userId]);
  const related = await run(f.userId,f.userId,f.agent); await admin.query("UPDATE space_runs SET state='running' WHERE id=$1", [related.id]);
  await expect(f.repository.purgeAgents(job)).rejects.toThrow('has not stopped');
  expect((await admin.query("SELECT 1 FROM misty_memories WHERE id=$1", [f.memory])).rowCount).toBe(1);
});
it("refuses inconsistent cross-account attachment ownership before a conversation cascade", async () => {
  const f = await fixture(), other = await account();
  await admin.query("UPDATE ai_conversation_attachments SET user_id=$2 WHERE id=$1", [f.invocation,other.userId]);
  await expect(f.repository.purgeAgents(await f.claim())).rejects.toThrow('ownership mismatch');
  expect((await admin.query("SELECT 1 FROM agent_conversations WHERE id=$1", [f.conversation])).rowCount).toBe(1);
});
it("rejects legacy ownership, reactivated accounts and caller cancellation", async () => {
  const f = await fixture(), job = await f.claim();
  await admin.query("UPDATE account_deletion_requests SET cleanup_owner='go' WHERE id=$1", [f.requestId]); expect(await f.repository.purgeAgents(job)).toBe(false);
  await admin.query("UPDATE account_deletion_requests SET cleanup_owner='native' WHERE id=$1", [f.requestId]);
  await admin.query("UPDATE users SET lifecycle_state='active' WHERE id=$1", [f.userId]); expect(await f.repository.purgeAgents(job)).toBe(false);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.userId]);
  const controller = new AbortController(); controller.abort(); await expect(f.repository.purgeAgents(job,controller.signal)).rejects.toThrow();
  expect((await admin.query("SELECT 1 FROM misty_memories WHERE id=$1", [f.memory])).rowCount).toBe(1);
});
