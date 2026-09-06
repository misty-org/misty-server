import {randomUUID} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {Pool} from 'pg';
import {afterAll,afterEach,beforeAll,expect,it} from 'vitest';
import {applyMigrations,readMigrations} from '../../../../../packages/database/src/migrations.js';
import {createTestDatabase} from '../../../../../packages/database/src/test-database.js';
import {withTransaction} from '../../../../../packages/database/src/transaction.js';
import {createMistyInvocationRepository} from './invocations.js';
import {cancelAccountAi} from './cancellation.js';
const admin=createTestDatabase(), users:string[]=[];
let application:Pool;
beforeAll(async()=>{
  await applyMigrations(admin,await readMigrations(fileURLToPath(new URL('../../../../../internal/platform/postgres/migrations/',import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_invocation_test') THEN CREATE ROLE misty_invocation_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_invocation_test;
    GRANT SELECT,UPDATE ON users,spaces,ai_user_settings,ai_invocations TO misty_invocation_test;
    GRANT SELECT,INSERT ON ai_invocation_events TO misty_invocation_test;`);
  application=new Pool({connectionString:process.env.MISTY_TEST_DATABASE_URL,options:'-c role=misty_invocation_test',max:3});
},60000);
afterEach(async()=>{await admin.query('DELETE FROM users WHERE id=ANY($1::text[])',[users]);users.length=0;});
afterAll(async()=>{if(application)await application.end();await admin.end();});
async function fixture(){
  const userId=`callback_${randomUUID().replaceAll('-','').slice(0,12)}`,invocationId=randomUUID(); users.push(userId);
  await withTransaction(admin,async tx=>{
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)",[userId,`${userId}@example.invalid`,`license_${userId}`]);
    await tx.query('INSERT INTO licenses(id,user_id) VALUES($1,$2)',[`license_${userId}`,userId]);
    await tx.query('INSERT INTO ai_user_settings(user_id) VALUES($1)',[userId]);
    await tx.query(`INSERT INTO ai_invocations(id,user_id,surface_id,mode,trigger_kind,state,idempotency_key,runtime_kind,runtime_run_id)
      VALUES($1,$2,'home','quick','message','running',$1,'agent-runtime','runtime-1')`,[invocationId,userId]);
  });
  const repo=createMistyInvocationRepository(application);
  const event={userId,invocationId,runtimeKind:'agent-runtime',runtimeRunId:'runtime-1',sequence:1,eventType:'result',payload:{answer:'done'},state:'completed' as const};
  return {userId,invocationId,repo,event};
}
it('persists ordered events atomically and accepts exact retry without replacing terminal output',async()=>{
  const f=await fixture();
  expect(await f.repo.appendRuntimeEvent({...f.event,sequence:2})).toBe(false);
  expect(await f.repo.appendRuntimeEvent(f.event)).toBe(true);
  expect(await f.repo.appendRuntimeEvent(f.event)).toBe(true);
  expect(await f.repo.appendRuntimeEvent({...f.event,payload:{answer:'changed'}})).toBe(false);
  expect(await f.repo.appendRuntimeEvent({...f.event,state:'running'})).toBe(false);
  expect(await f.repo.appendRuntimeEvent({...f.event,sequence:2,state:'running'})).toBe(false);
  expect((await admin.query('SELECT state FROM ai_invocations WHERE id=$1',[f.invocationId])).rows[0].state).toBe('completed');
  expect((await admin.query('SELECT sequence FROM ai_invocation_events WHERE invocation_id=$1',[f.invocationId])).rows).toEqual([{sequence:'1'}]);
});
it('rejects stale runtime identities, expired invocations and disabled AI settings',async()=>{
  const f=await fixture();
  expect(await f.repo.appendRuntimeEvent({...f.event,runtimeRunId:'old-runtime'})).toBe(false);
  expect(await f.repo.appendRuntimeEvent({...f.event,userId:'another-account'})).toBe(false);
  await admin.query("UPDATE ai_invocations SET expires_at=now()-interval '1 second' WHERE id=$1",[f.invocationId]);
  expect(await f.repo.appendRuntimeEvent(f.event)).toBe(false);
  await admin.query('UPDATE ai_invocations SET expires_at=NULL WHERE id=$1',[f.invocationId]);
  await admin.query('UPDATE ai_user_settings SET enabled=false WHERE user_id=$1',[f.userId]);
  expect(await f.repo.appendRuntimeEvent(f.event)).toBe(false);
});
it('waits for the account lock and rejects late completion after cancellation commits',async()=>{
  const f=await fixture(), blocker=await admin.connect(); let pending:Promise<boolean>|undefined;
  try{
    await blocker.query('BEGIN'); await blocker.query("SET LOCAL app.rls_mode='service'");
    await blocker.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1",[f.userId]);
    pending=f.repo.appendRuntimeEvent(f.event);
    await cancelAccountAi(blocker,f.userId,new AbortController().signal);
    await blocker.query('COMMIT'); expect(await pending).toBe(false);
  }finally{await blocker.query('ROLLBACK');blocker.release();if(pending)await pending;}
  expect((await admin.query('SELECT state,error_code FROM ai_invocations WHERE id=$1',[f.invocationId])).rows[0]).toEqual({state:'canceled',error_code:'account_disabled'});
  expect((await admin.query('SELECT 1 FROM ai_invocation_events WHERE invocation_id=$1',[f.invocationId])).rowCount).toBe(0);
});
it('rolls back cancellation and event writes when their transaction fails',async()=>{
  const f=await fixture();
  await expect(withTransaction(admin,async tx=>{
    await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1",[f.userId]);
    await cancelAccountAi(tx,f.userId,new AbortController().signal); throw new Error('rollback');
  },{mode:'service'})).rejects.toThrow('rollback');
  expect((await admin.query('SELECT enabled FROM ai_user_settings WHERE user_id=$1',[f.userId])).rows[0].enabled).toBe(true);
  await admin.query('REVOKE UPDATE ON ai_invocations FROM misty_invocation_test');
  try{await expect(f.repo.appendRuntimeEvent(f.event)).rejects.toMatchObject({code:'42501'});}
  finally{await admin.query('GRANT UPDATE ON ai_invocations TO misty_invocation_test');}
  expect((await admin.query('SELECT 1 FROM ai_invocation_events WHERE invocation_id=$1',[f.invocationId])).rowCount).toBe(0);
});
