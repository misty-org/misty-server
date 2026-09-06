import { randomUUID } from "node:crypto";
import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import type { EmailSender } from "../../../../../../packages/email/src/mailjet.js";
import { hashToken } from "../../auth/service.js";
import { invitationToken, type InvitationConfig } from "./config.js";
type Job = { invite_id: string; generation: string; token_key_id: string; attempts: number; space_id: string };
const html = (value: string) => value.replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]!);
export function createInvitationJobs(pool: Pool, config: InvitationConfig, send: EmailSender) {
  return {
    async runOnce() {
      const lease = randomUUID();
      const jobs = await withTransaction(pool, async (tx) => {
        // Expiry is a bounded cleanup operation, never a side effect of listing.
        await tx.query(`WITH expired AS (SELECT id FROM space_invitations WHERE expires_at<=now() AND consumed_at IS NULL AND revoked_at IS NULL
          ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT 25) UPDATE space_invitations i SET revoked_at=now() FROM expired e WHERE i.id=e.id`);
        return (await tx.query<Job>(`WITH due AS (SELECT j.invite_id FROM space_invitation_delivery_jobs j
          WHERE (j.state='pending' AND j.available_at<=now()) OR (j.state='processing' AND j.lease_expires_at<=now())
          ORDER BY j.available_at,j.invite_id FOR UPDATE SKIP LOCKED LIMIT 4), claimed AS (
          UPDATE space_invitation_delivery_jobs j SET state='processing',lease_id=$1,lease_expires_at=now()+interval '1 minute',attempts=attempts+1,updated_at=now()
          FROM due WHERE j.invite_id=due.invite_id RETURNING j.*)
          SELECT c.invite_id,c.generation,c.token_key_id,c.attempts,i.space_id FROM claimed c JOIN space_invitations i ON i.id=c.invite_id`, [lease])).rows;
      }, { mode: "service" });
      for (const job of jobs) {
        try {
          const ready = await withTransaction(pool, async (tx) => {
            await tx.query("SELECT id FROM spaces WHERE id=$1 FOR SHARE", [job.space_id]);
            const invite = (await tx.query<{ invited_email: string; token_hash: string; space_name: string; inviter_name: string; live: boolean }>(`SELECT i.invited_email,i.token_hash,s.name AS space_name,u.name AS inviter_name,
              (i.revoked_at IS NULL AND i.consumed_at IS NULL AND i.expires_at>now() AND s.lifecycle_state='active' AND u.lifecycle_state='active') AS live
              FROM space_invitations i JOIN spaces s ON s.id=i.space_id JOIN users u ON u.id=i.invited_by_user_id WHERE i.id=$1 FOR SHARE OF i`, [job.invite_id])).rows[0];
            const current = await tx.query(`SELECT invite_id FROM space_invitation_delivery_jobs WHERE invite_id=$1 AND generation=$2 AND state='processing' AND lease_id=$3 AND lease_expires_at>now() FOR UPDATE`, [job.invite_id, job.generation, lease]);
            if (!current.rowCount) return null;
            const token = invite?.live ? invitationToken(config.keys, job.token_key_id, job.invite_id, job.generation, invite.invited_email) : "";
            if (!invite?.live || invite.token_hash !== hashToken(token)) {
              await tx.query("UPDATE space_invitation_delivery_jobs SET state='superseded',lease_id=NULL,lease_expires_at=NULL,updated_at=now() WHERE invite_id=$1", [job.invite_id]); return null;
            }
            return { ...invite, token };
          }, { mode: "service" });
          if (!ready) continue;
          const link = `${config.baseUrl}/${encodeURIComponent(ready.token)}`;
          await send({ to: ready.invited_email, subject: "You've been invited to a Misty Space",
            text: `${ready.inviter_name} invited you to ${ready.space_name}.\n\nJoin the Space: ${link}\n\nThis link expires seven days after it was issued.`,
            html: `<p>${html(ready.inviter_name)} invited you to ${html(ready.space_name)}.</p><p><a href="${html(link)}">Join the Space</a></p><p>This link expires seven days after it was issued.</p>` });
          await finish(job, true);
        } catch { await finish(job, false); }
      }
      return jobs.length > 0;
      async function finish(job: Job, sent: boolean) {
        await withTransaction(pool, async (tx) => {
          // Lock invitation before job, matching issue/accept and avoiding the
          // job->invitation inversion after an external provider call.
          await tx.query("SELECT id FROM spaces WHERE id=$1 FOR SHARE", [job.space_id]);
          await tx.query("SELECT id FROM space_invitations WHERE id=$1 FOR UPDATE", [job.invite_id]);
          const updated = await tx.query(`UPDATE space_invitation_delivery_jobs SET state=$4,lease_id=NULL,lease_expires_at=NULL,
            last_error=$5,available_at=now()+LEAST(3600,30*power(2,LEAST(attempts,7)))*interval '1 second',updated_at=now()
            WHERE invite_id=$1 AND generation=$2 AND state='processing' AND lease_id=$3`,
            [job.invite_id, job.generation, lease, sent ? "sent" : "pending", sent ? "" : "invitation_delivery_failed"]);
          if (updated.rowCount) await tx.query("UPDATE space_invitations SET delivery_status=$2,last_sent_at=now() WHERE id=$1", [job.invite_id, sent ? "sent" : "failed"]);
        }, { mode: "service" });
      }
    },
  };
}
