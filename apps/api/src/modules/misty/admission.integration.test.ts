import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createMistyAdmissionRepository, type InvocationInput } from "./admission.js";
import { createMistyInvocationRepository } from "./invocations.js";
import { cancelAccountAi } from "./cancellation.js";
const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], domains: string[] = [];
let application: Pool;
beforeAll(async () => {
  await applyMigrations(admin,await readMigrations(fileURLToPath(new URL('../../../../../internal/platform/postgres/migrations/',import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_admission_test') THEN CREATE ROLE misty_admission_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_admission_test;
    GRANT SELECT,UPDATE ON users,spaces,space_members,sessions,agent_conversations TO misty_admission_test;
    GRANT SELECT,INSERT,UPDATE ON ai_user_settings,ai_invocations TO misty_admission_test;
    GRANT SELECT,INSERT ON ai_invocation_events TO misty_admission_test;`);
  application = new Pool({ connectionString:process.env.MISTY_TEST_DATABASE_URL,options:'-c role=misty_admission_test',max:5 });
},60000);
afterEach(async () => {
  await withTransaction(admin,async tx => {
    await tx.query('DELETE FROM spaces WHERE id=ANY($1::text[])',[spaces]);
    await tx.query('DELETE FROM security_domains WHERE id=ANY($1::text[])',[domains]);
    await tx.query('DELETE FROM users WHERE id=ANY($1::text[])',[users]);
  }); users.length=0; spaces.length=0; domains.length=0;
});
afterAll(async () => { if(application)await application.end(); await admin.end(); });
async function fixture() {
  const userId=`admit_${randomUUID().replaceAll('-','').slice(0,12)}`,sessionHash=randomUUID(); users.push(userId);
  await withTransaction(admin,async tx => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)",[userId,`${userId}@example.invalid`,`license_${userId}`]);
    await tx.query('INSERT INTO licenses(id,user_id) VALUES($1,$2)',[`license_${userId}`,userId]);
    await tx.query("INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 day')",[sessionHash,userId]);
  });
  const actor={userId,sessionHash},repo=createMistyAdmissionRepository(application),callbacks=createMistyInvocationRepository(application);
  const input:InvocationInput={surfaceId:'home',mode:'quick',trigger:'message',idempotencyKey:randomUUID(),payload:{prompt:'private prompt',context:{a:1,b:2}}};
  const runtime=(invocationId:string,runtimeRunId=randomUUID())=>({userId,invocationId,runtimeKind:'agent-runtime',runtimeRunId});
  return {userId,sessionHash,actor,repo,callbacks,input,runtime};
}
async function space(userId:string) {
  const id=randomUUID(),domain=randomUUID();spaces.push(id);domains.push(domain);
  await withTransaction(admin,async tx=>{
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)",[domain,userId,id]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Admission',$3)",[id,userId,domain]);
    await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner')",[id,userId]);
  }); return id;
}
it('creates one queued invocation under concurrent exact retries and does not extend its lifetime',async()=>{
  const f=await fixture(); const results=await Promise.all([f.repo.create(f.actor,f.input),f.repo.create(f.actor,f.input)]);
  expect(results.filter(r=>r.created)).toHaveLength(1); expect(results[0]!.invocation.id).toBe(results[1]!.invocation.id);
  const invocation=results[0]!.invocation;expect(invocation.state).toBe('queued');expect(invocation.id).toMatch(/^invocation_/);
  expect(invocation.expires_at!.getTime()-Date.now()).toBeGreaterThan(23*3600000);
  const retry=await f.repo.create(f.actor,{...f.input,payload:{context:{b:2,a:1},prompt:'private prompt'}});
  expect(retry.created).toBe(false);expect(retry.invocation.expires_at).toEqual(invocation.expires_at);
  await expect(f.repo.create(f.actor,{...f.input,payload:{prompt:'different'}})).rejects.toMatchObject({code:'idempotency_conflict'});
  expect((await admin.query('SELECT request_payload FROM ai_invocations WHERE id=$1',[invocation.id])).rows[0].request_payload).toEqual(f.input.payload);
});
it('requires the exact account session and rejects disabled account or AI settings',async()=>{
  const f=await fixture(),other=await fixture();
  await expect(f.repo.create({...f.actor,sessionHash:other.sessionHash},f.input)).rejects.toMatchObject({code:'unavailable'});
  await admin.query('INSERT INTO ai_user_settings(user_id,enabled) VALUES($1,false)',[f.userId]);
  await expect(f.repo.create(f.actor,f.input)).rejects.toMatchObject({code:'unavailable'});
  await admin.query('UPDATE ai_user_settings SET enabled=true WHERE user_id=$1',[f.userId]);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1",[f.userId]);
  await expect(f.repo.create(f.actor,f.input)).rejects.toMatchObject({code:'unavailable'});
  expect((await admin.query('SELECT 1 FROM ai_invocations WHERE user_id=$1',[f.userId])).rowCount).toBe(0);
});
it('enforces Space membership and conversation ownership/binding without leaking an old request',async()=>{
  const f=await fixture(),other=await fixture(),owned=await space(f.userId),foreign=await space(other.userId),conversation=`conversation_${randomUUID()}`;
  await expect(f.repo.create(f.actor,{...f.input,spaceId:foreign})).rejects.toMatchObject({code:'unavailable'});
  await admin.query('INSERT INTO agent_conversations(id,user_id,space_id) VALUES($1,$2,$3)',[conversation,other.userId,foreign]);
  await expect(f.repo.create(f.actor,{...f.input,spaceId:owned,conversationId:conversation})).rejects.toMatchObject({code:'unavailable'});
  await admin.query('UPDATE agent_conversations SET user_id=$2,space_id=$3 WHERE id=$1',[conversation,f.userId,owned]);
  const created=await f.repo.create(f.actor,{...f.input,spaceId:owned,conversationId:conversation});expect(created.created).toBe(true);
  await expect(f.repo.create(f.actor,{...f.input,conversationId:conversation})).rejects.toMatchObject({code:'unavailable'});
});
it('allows one runtime binding under competing activation and never replaces its identity',async()=>{
  const f=await fixture(),created=await f.repo.create(f.actor,f.input),a=f.runtime(created.invocation.id),b=f.runtime(created.invocation.id);
  const results=await Promise.all([f.repo.activate(a),f.repo.activate(b)]);expect(results.filter(Boolean)).toHaveLength(1);
  const winner=results[0]?a:b,loser=results[0]?b:a;
  expect((await f.repo.activate(winner))?.runtime_run_id).toBe(winner.runtimeRunId);expect(await f.repo.activate(loser)).toBeNull();
  expect(await f.repo.activate({...winner,runtimeKind:'different'})).toBeNull();
  expect(await f.callbacks.appendRuntimeEvent({...winner,sequence:1,eventType:'approval',payload:{},state:'awaiting_approval'})).toBe(true);
  expect((await f.repo.activate(winner))?.state).toBe('awaiting_approval');
});
it('refuses activation and callbacks after membership loss, account cancellation or expiry',async()=>{
  const f=await fixture(),owned=await space(f.userId),created=await f.repo.create(f.actor,{...f.input,spaceId:owned}),runtime=f.runtime(created.invocation.id);
  expect(await f.repo.activate(runtime)).not.toBeNull();
  await admin.query('DELETE FROM space_members WHERE space_id=$1 AND user_id=$2',[owned,f.userId]);
  expect(await f.repo.activate(runtime)).toBeNull();expect(await f.callbacks.appendRuntimeEvent({...runtime,sequence:1,eventType:'result',payload:{},state:'completed'})).toBe(false);
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner')",[owned,f.userId]);
  await admin.query("UPDATE ai_invocations SET expires_at=now()-interval '1 second' WHERE id=$1",[created.invocation.id]);expect(await f.repo.activate(runtime)).toBeNull();
  await admin.query("UPDATE ai_invocations SET expires_at=now()+interval '1 day' WHERE id=$1",[created.invocation.id]);
  await withTransaction(admin,async tx=>{await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1",[f.userId]);await cancelAccountAi(tx,f.userId,new AbortController().signal);},{mode:'service'});
  expect(await f.repo.activate(runtime)).toBeNull();
});
it('rolls back creation if the exact session expires while SQL is executing',async()=>{
  const f=await fixture();
  await admin.query(`CREATE FUNCTION misty_test_admission_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.user_id='${f.userId}' THEN PERFORM pg_sleep(0.35); END IF; RETURN NEW; END $$;
    CREATE TRIGGER misty_test_admission_delay BEFORE INSERT ON ai_invocations FOR EACH ROW EXECUTE FUNCTION misty_test_admission_delay()`);
  try{
    await admin.query("UPDATE sessions SET expires_at=clock_timestamp()+interval '0.2 seconds' WHERE token_hash=$1",[f.sessionHash]);
    await expect(f.repo.create(f.actor,f.input)).rejects.toMatchObject({code:'unavailable'});
    expect((await admin.query('SELECT 1 FROM ai_invocations WHERE user_id=$1',[f.userId])).rowCount).toBe(0);
  }finally{await admin.query('DROP TRIGGER misty_test_admission_delay ON ai_invocations; DROP FUNCTION misty_test_admission_delay()');}
});
it('rolls back an event receipt when its invocation expires during insertion',async()=>{
  const f=await fixture(),created=await f.repo.create(f.actor,f.input),runtime=f.runtime(created.invocation.id);await f.repo.activate(runtime);
  await admin.query(`CREATE FUNCTION misty_test_event_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.invocation_id='${created.invocation.id}' THEN PERFORM pg_sleep(0.35); END IF; RETURN NEW; END $$;
    CREATE TRIGGER misty_test_event_delay BEFORE INSERT ON ai_invocation_events FOR EACH ROW EXECUTE FUNCTION misty_test_event_delay()`);
  try{
    await admin.query("UPDATE ai_invocations SET expires_at=clock_timestamp()+interval '0.2 seconds' WHERE id=$1",[created.invocation.id]);
    await expect(f.callbacks.appendRuntimeEvent({...runtime,sequence:1,eventType:'result',payload:{},state:'completed'})).rejects.toThrow('expired');
    expect((await admin.query('SELECT 1 FROM ai_invocation_events WHERE invocation_id=$1',[created.invocation.id])).rowCount).toBe(0);
  }finally{await admin.query('DROP TRIGGER misty_test_event_delay ON ai_invocation_events; DROP FUNCTION misty_test_event_delay()');}
});
it('refuses activation and completion after its private conversation is deleted',async()=>{
  const f=await fixture(),conversationId=`conversation_${randomUUID()}`;
  await admin.query('INSERT INTO agent_conversations(id,user_id) VALUES($1,$2)',[conversationId,f.userId]);
  const created=await f.repo.create(f.actor,{...f.input,conversationId}),runtime=f.runtime(created.invocation.id);
  expect(await f.repo.activate(runtime)).not.toBeNull();
  await admin.query('UPDATE agent_conversations SET deleted_at=now() WHERE id=$1',[conversationId]);
  expect(await f.repo.activate(runtime)).toBeNull();
  expect(await f.callbacks.appendRuntimeEvent({...runtime,sequence:1,eventType:'result',payload:{},state:'completed'})).toBe(false);
  expect((await admin.query('SELECT 1 FROM ai_invocation_events WHERE invocation_id=$1',[created.invocation.id])).rowCount).toBe(0);
});
