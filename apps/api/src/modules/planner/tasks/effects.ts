import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { requirePlannerActor } from "../access.js";
import { SpaceError } from "../../spaces/model.js";
import { createAssignedTaskRun } from "./assignment.js";
import { claimTaskWorkflows } from "./workflow-claims.js";
import { TaskEffectInvalid, type TaskEffect } from "./effects-model.js";

export function createTaskEffects(pool: Pool) {
  async function process(id: string): Promise<boolean> {
    return withTransaction(pool, async tx => {
      await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
      const parent = (await tx.query<TaskEffect>("SELECT * FROM native_task_effects WHERE id=$1 AND state='pending' AND available_at<=now()", [id])).rows[0];
      if (!parent) return false;
      // Follow the same Space/account order as mutations and account deletion.
      let allowed = true;
      try { await requirePlannerActor(tx, { userId: parent.actor_user_id }, parent.space_id, "tasks.write", true); }
      catch (error) { if (!(error instanceof SpaceError)) throw error; allowed = false; }
      const effect = (await tx.query<TaskEffect>(`SELECT * FROM native_task_effects WHERE id=$1 AND state='pending'
        AND available_at<=now() AND (lease_until IS NULL OR lease_until<=now()) FOR UPDATE SKIP LOCKED`, [id])).rows[0];
      if (!effect) return false;
      if (!allowed) {
        await tx.query("UPDATE native_task_effects SET state='canceled',completed_at=now(),last_error_code='task_actor_unavailable',lease_id=NULL,lease_until=NULL WHERE id=$1", [id]);
        return true;
      }
      // One database transaction is the claim: no external work or detached
      // promise can outlive it. A crash rolls back both fan-out and completion.
      await createAssignedTaskRun(tx, effect);
      await claimTaskWorkflows(tx, effect);
      await tx.query("UPDATE native_task_effects SET state='completed',completed_at=now(),attempts=attempts+1,last_error_code=NULL,lease_id=NULL,lease_until=NULL WHERE id=$1", [id]);
      return true;
    }, { mode: "service" });
  }
  return {
    process,
    async runOnce() {
      const next = await withTransaction(pool, tx => tx.query<{ id: string }>(`SELECT id FROM native_task_effects WHERE state='pending'
        AND available_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY available_at,id LIMIT 1`), { mode: "service" });
      const id = next.rows[0]?.id;
      if (!id) return false;
      try { return await process(id); }
      catch (error) {
        await withTransaction(pool, tx => tx.query(`UPDATE native_task_effects SET attempts=attempts+1,
          available_at=now()+interval '30 seconds',last_error_code=$2 WHERE id=$1 AND state='pending'`,
        [id, error instanceof TaskEffectInvalid ? error.message : "task_effect_retry"]), { mode: "service" });
        throw error;
      }
    },
  };
}
