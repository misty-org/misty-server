import type { Pool } from "pg";
import type { DeletionJob } from "../jobs.js";
import { runPurgePhase } from "./phase.js";

// Account-private state only. Apps, providers, devices, Library processing,
// shared content and billing have separate retention/reconciliation obligations.
export const privateAccountTables = [
  "ai_recaps", "ai_invocation_contexts", "misty_conversation_focus", "misty_conversation_pending_actions",
  "user_home_activity", "space_inbox_items", "space_conversation_reads", "space_library_item_views",
  "space_action_suggestion_dismissals", "space_conversation_suggestion_vetoes", "realtime_tickets",
  "library_reauthentication_grants", "space_resolve_tickets", "sessions", "auth_handoff_tokens", "password_reset_tokens",
] as const;
const accountSpaces = `SELECT space_id AS id FROM ai_invocation_contexts WHERE user_id=$1
  UNION SELECT space_id FROM misty_conversation_focus WHERE user_id=$1
  UNION SELECT space_id FROM misty_conversation_pending_actions WHERE user_id=$1
  UNION SELECT space_id FROM user_home_activity WHERE user_id=$1
  UNION SELECT space_id FROM space_inbox_items WHERE user_id=$1
  UNION SELECT space_id FROM space_library_item_views WHERE user_id=$1
  UNION SELECT space_id FROM library_reauthentication_grants WHERE user_id=$1
  UNION SELECT space_id FROM space_resolve_tickets WHERE user_id=$1
  UNION SELECT c.space_id FROM space_conversation_reads r JOIN space_conversations c ON c.id=r.conversation_id WHERE r.user_id=$1
  UNION SELECT c.space_id FROM space_conversation_suggestion_vetoes v JOIN space_conversations c ON c.id=v.conversation_id WHERE v.user_id=$1
  UNION SELECT b.space_id FROM space_action_suggestion_dismissals d JOIN space_action_suggestion_batches b ON b.id=d.batch_id WHERE d.user_id=$1`;

export function createDeletionAccountPurgeRepository(pool: Pool) {
  return { purgeAccountState(job: DeletionJob, signal?: AbortSignal) {
    return runPurgePhase({ pool, job, spacesQuery: accountSpaces, receipt: { account_private_state: "purged" },
      ...(signal ? { signal } : {}), erase: async (tx, deadline) => {
        // Retain unresolved execution evidence. Recap cancellation/claim writers
        // must take the account lock before this phase can be enabled.
        if ((await tx.query(`SELECT 1 FROM ai_recaps WHERE user_id=$1 AND (state='running' OR lease_until>clock_timestamp())
          UNION ALL SELECT 1 FROM ai_invocations WHERE user_id=$1 AND state IN ('queued','running','awaiting_approval') LIMIT 1`, [job.user_id])).rowCount) throw new Error("Account AI work has not stopped");
        for (const table of privateAccountTables) {
          deadline.throwIfAborted(); await tx.query(`DELETE FROM ${table} WHERE user_id=$1`, [job.user_id]);
        }
      } });
  } };
}
