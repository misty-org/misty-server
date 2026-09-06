import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";

type Identity = { user_id: string; app_id: string };
export type AppPurgeClaim = Identity & { attempts: number; started_at: string };
// Backoff uses the existing updated_at column so old pending/running jobs migrate
// without changing the recovery deadline or requiring a second queue.
const due = `j.delete_at<=now() AND (
  (i.state='recoverable' AND (j.state='pending' OR (j.state='failed' AND j.updated_at<=now()-LEAST(3600,30*power(2,LEAST(j.attempts,7)))*interval '1 second')))
  OR (i.state='purging' AND j.state='running' AND j.started_at<=now()-interval '15 minutes'))`;
async function lockInstallation(tx: PoolClient, job: Identity) {
  if (!(await tx.query("SELECT id FROM users WHERE id=$1 FOR SHARE", [job.user_id])).rowCount) return false;
  await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`apps:install:${job.user_id}:${job.app_id}`]);
  return !!(await tx.query("SELECT app_id FROM user_app_installations WHERE user_id=$1 AND app_id=$2 FOR UPDATE", [job.user_id, job.app_id])).rowCount;
}
async function event(tx: PoolClient, job: AppPurgeClaim, type: string) {
  await tx.query("INSERT INTO app_install_events(user_id,app_id,event_type,metadata) VALUES($1,$2,$3,$4::jsonb)",
    [job.user_id, job.app_id, type, JSON.stringify({ attempt: job.attempts, ...(type === "purge_failed" ? { error: "app_data_purge_failed" } : {}) })]);
}
export function createAppPurgeRepository(pool: Pool) {
  const tx = <T>(run: (client: PoolClient) => Promise<T>) => withTransaction(pool, run, { mode: "service" });
  return {
    async claim(): Promise<AppPurgeClaim[]> {
      const candidates = await tx(async (client) => (await client.query<Identity>(`SELECT j.user_id,j.app_id FROM app_data_deletion_jobs j
        JOIN user_app_installations i USING(user_id,app_id) WHERE ${due} ORDER BY j.delete_at,j.user_id,j.app_id LIMIT 25`)).rows);
      const claims: AppPurgeClaim[] = [];
      for (const candidate of candidates) {
        const claimed = await tx(async (client) => {
          if (!await lockInstallation(client, candidate)) return null;
          const row = (await client.query(`SELECT j.user_id FROM app_data_deletion_jobs j JOIN user_app_installations i USING(user_id,app_id)
            WHERE j.user_id=$1 AND j.app_id=$2 AND ${due} FOR UPDATE OF j`, [candidate.user_id, candidate.app_id])).rows[0];
          if (!row) return null;
          const claim = (await client.query<AppPurgeClaim>(`UPDATE app_data_deletion_jobs SET state='running',attempts=attempts+1,last_error='',started_at=now(),updated_at=now()
            WHERE user_id=$1 AND app_id=$2 RETURNING user_id,app_id,attempts,started_at::text`, [candidate.user_id, candidate.app_id])).rows[0]!;
          await client.query("UPDATE user_app_installations SET state='purging',pinned=false,updated_at=now() WHERE user_id=$1 AND app_id=$2", [candidate.user_id, candidate.app_id]);
          await event(client, claim, "purge_started");
          return claim;
        });
        if (claimed) claims.push(claimed);
      }
      return claims;
    },
    complete: (job: AppPurgeClaim) => tx(async (client) => {
      if (!await lockInstallation(client, job)) return false;
      const current = await client.query(`SELECT j.user_id FROM app_data_deletion_jobs j JOIN user_app_installations i USING(user_id,app_id)
        WHERE j.user_id=$1 AND j.app_id=$2 AND j.state='running' AND i.state='purging' AND j.attempts=$3 AND j.started_at=$4 FOR UPDATE OF j`,
      [job.user_id, job.app_id, job.attempts, job.started_at]);
      if (!current.rowCount) return false;
      // Only account-private app namespaces. Space notes, files and tasks are
      // collaborative data and must survive a member uninstalling an app.
      for (const table of ["app_personal_records", "app_runtime_sessions", "user_app_activity"]) await client.query(`DELETE FROM ${table} WHERE user_id=$1 AND app_id=$2`, [job.user_id, job.app_id]);
      await client.query("UPDATE user_app_installations SET state='purged',pinned=false,purged_at=now(),updated_at=now() WHERE user_id=$1 AND app_id=$2", [job.user_id, job.app_id]);
      await client.query("UPDATE app_data_deletion_jobs SET state='completed',completed_at=now(),updated_at=now(),last_error='' WHERE user_id=$1 AND app_id=$2", [job.user_id, job.app_id]);
      await event(client, job, "purged");
      return true;
    }),
    fail: (job: AppPurgeClaim) => tx(async (client) => {
      if (!await lockInstallation(client, job)) return false;
      const changed = await client.query(`UPDATE app_data_deletion_jobs SET state='failed',last_error='app_data_purge_failed',updated_at=now()
        WHERE user_id=$1 AND app_id=$2 AND state='running' AND attempts=$3 AND started_at=$4`, [job.user_id, job.app_id, job.attempts, job.started_at]);
      if (!changed.rowCount) return false;
      // Completion is one SQL transaction: failure cannot leave partially purged
      // private data. Restoration remains safe until a successful retry starts.
      await client.query("UPDATE user_app_installations SET state='recoverable',updated_at=now() WHERE user_id=$1 AND app_id=$2 AND state='purging'", [job.user_id, job.app_id]);
      await event(client, job, "purge_failed");
      return true;
    }),
  };
}
export function createAppPurgeJobs(pool: Pool) {
  const repository = createAppPurgeRepository(pool);
  return {
    async runOnce() {
      const claims = await repository.claim();
      for (const claim of claims) {
        try { await repository.complete(claim); }
        catch { await repository.fail(claim); }
      }
      return claims.length > 0;
    },
  };
}
