import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { SpaceActor } from "../spaces/access.js";
import { requireConnectionActor } from "../connections/access.js";
import { SpaceError } from "../spaces/model.js";
import { MailError } from "./errors.js";

export type MailAuditIntent = { connectionId: string; action: "thread_modify" | "draft_create" | "draft_update" | "draft_send";
  targetType: "thread" | "draft"; targetId: string; source: "user" | "ai"; confirmed: boolean };
export function createMailAudit(pool: Pick<Pool, "connect">) {
  return {
    // Call only while holding this draft's operation lock, before inserting the
    // next intent. A lost send response is not permission to send it again.
    assertDraftSettled: (userId: string, connectionId: string, draftId: string) => withTransaction(pool, async (tx) => {
      if ((await tx.query(`SELECT id FROM mail_action_audit WHERE user_id=$1 AND connection_id=$2 AND target_type='draft' AND target_id=$3
        AND (completed_at IS NULL OR error_code='mail_operation_incomplete' OR (action='draft_send' AND success)) LIMIT 1`, [userId, connectionId, draftId])).rowCount)
        throw new MailError("mail_draft_reconciliation_required");
    }, { mode: "service" }),
    identifyDraft: (userId: string, id: string, draftId: string) => withTransaction(pool, async (tx) => {
      const result = await tx.query("UPDATE mail_action_audit SET target_id=$3 WHERE id=$1 AND user_id=$2 AND target_type='draft' AND completed_at IS NULL", [id, userId, draftId]);
      if (result.rowCount !== 1) throw new Error("Mail audit draft identity unavailable");
    }, { mode: "service" }),
    begin: (actor: SpaceActor, intent: MailAuditIntent) => withTransaction(pool, async (tx) => {
      await requireConnectionActor(tx, actor, "mail.write");
      if (!(await tx.query("SELECT id FROM connected_accounts WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND capabilities ? 'mail' FOR SHARE", [intent.connectionId, actor.userId])).rowCount) throw new SpaceError("not_found");
      return (await tx.query<{ id: string }>(`INSERT INTO mail_action_audit(user_id,connection_id,action,target_type,target_id,source,confirmed,success,error_code,completed_at)
        VALUES($1,$2,$3,$4,$5,$6,$7,false,'mail_operation_pending',NULL) RETURNING id`,
      [actor.userId, intent.connectionId, intent.action, intent.targetType, intent.targetId, intent.source, intent.confirmed])).rows[0]!.id;
    }, { mode: "service" }),
    // Completion must survive cancellation or App revocation after dispatch.
    // The already committed intent supplies ownership; no mail data is stored.
    finish: (userId: string, id: string, success: boolean, errorCode = "", targetId?: string) => withTransaction(pool, async (tx) => {
      const result = await tx.query(`UPDATE mail_action_audit SET success=$3,error_code=$4,completed_at=now(),target_id=COALESCE($5,target_id)
        WHERE id=$1 AND user_id=$2 AND completed_at IS NULL`, [id, userId, success, errorCode, targetId ?? null]);
      if (result.rowCount !== 1) throw new Error("Mail audit completion unavailable");
    }, { mode: "service" }),
  };
}
