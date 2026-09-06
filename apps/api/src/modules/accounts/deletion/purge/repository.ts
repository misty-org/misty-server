import type { Pool } from "pg";
import { runPurgePhase } from "./phase.js";
import type { DeletionJob } from "../jobs.js";
import { agentPurgeSpaces, privateRuns, purgePrivateAgentData } from "./agents.js";

/** One phase of retention purge. It deliberately does not mark the account or
 * the purge step completed: storage, owned Spaces and other private modules
 * require their own verified effects before final anonymization. */
export function createDeletionPurgeRepository(pool: Pool) {
  return { async purgeAgents(job: DeletionJob, callerSignal: AbortSignal = new AbortController().signal): Promise<boolean> {
    return runPurgePhase({ pool, job, spacesQuery: agentPurgeSpaces, signal: callerSignal,
      receipt: { agent_data: "purged", attachment_objects: "queued" }, erase: async (tx, signal) => {
      // Erasure cannot race a still-running tool. Initiation/local cleanup must
      // cancel work first; service writers must enforce account lifecycle too.
      if ((await tx.query(`SELECT id FROM space_runs WHERE (${privateRuns} OR agent_id IN (SELECT id FROM personal_agents WHERE owner_user_id=$1))
        AND state IN ('queued','running','cooldown','retrying','awaiting_approval','awaiting_device') LIMIT 1`, [job.user_id])).rowCount) throw new Error("Agent work has not stopped");
      if ((await tx.query(`SELECT id FROM ai_invocations WHERE user_id=$1 AND state IN ('queued','running','awaiting_approval') LIMIT 1`, [job.user_id])).rowCount) throw new Error("AI invocation has not stopped");
      if ((await tx.query(`SELECT 1 FROM agent_run_jobs j JOIN space_runs r ON r.id=j.run_id
        WHERE j.state IN ('queued','leased','dispatched') AND (r.owner_user_id=$1 OR r.requesting_member_id=$1 OR r.billing_user_id=$1 OR r.initiated_by_user_id=$1
          OR r.agent_id IN (SELECT id FROM personal_agents WHERE owner_user_id=$1))
        UNION ALL SELECT 1 FROM workflow_device_node_jobs WHERE user_id=$1 AND state IN ('queued','leased')
        UNION ALL SELECT 1 FROM ai_artifacts WHERE user_id=$1 AND state='applying'
        UNION ALL SELECT 1 FROM agent_toolbox_action_journal WHERE user_id=$1 AND state='started' LIMIT 1`, [job.user_id])).rowCount) throw new Error("Agent side effect has not stopped");
      await purgePrivateAgentData(tx, job.user_id, signal);
    } });
  } };
}
