import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { hashToken } from "../../auth/service.js";
import { requireSpaceActor, requireActiveAccount } from "../access.js";
import { readSpace } from "../read.js";
import { spaceEvent } from "../templates.js";
import { SpaceError } from "../model.js";
import { invitationToken, type InvitationConfig } from "./config.js";
export class InvitationError extends Error {
  constructor(readonly code: "invite_not_found" | "invite_expired" | "invitation_unavailable") { super(code); }
}
const select = `SELECT i.id,i.space_id,s.name AS space_name,i.invited_user_id,invited.name AS invited_user_name,i.invited_email,
  i.invited_by_user_id,inviter.name AS inviter_name,i.delivery_status,i.expires_at,i.created_at FROM space_invitations i JOIN spaces s ON s.id=i.space_id
  JOIN users inviter ON inviter.id=i.invited_by_user_id LEFT JOIN users invited ON invited.id=i.invited_user_id`;
async function read(tx: PoolClient, id: string) { const row = (await tx.query(`${select} WHERE i.id=$1`, [id])).rows[0]; if (!row) throw new InvitationError("invite_not_found"); return row; }
export function createInvitationRepository(pool: Pool, config: InvitationConfig | null) {
  return {
    list: (userId: string, spaceId: string) => withTransaction(pool, async (tx) => {
      await requireSpaceActor(tx, { userId }, spaceId, true);
      return { invitations: (await tx.query(`${select} WHERE i.space_id=$1 AND i.revoked_at IS NULL AND i.consumed_at IS NULL AND i.expires_at>now() ORDER BY i.created_at DESC,i.id`, [spaceId])).rows };
    }, { mode: "service" }),
    issue: (userId: string, spaceId: string, email: string | null, existingId?: string) => withTransaction(pool, async (tx) => {
      await requireSpaceActor(tx, { userId }, spaceId, true);
      if (!config) throw new InvitationError("invitation_unavailable");
      const id = existingId ?? `invite_${randomUUID()}`, generation = randomUUID();
      let recipient: { id: string | null; name: string | null; email: string };
      if (existingId) {
        const previous = (await tx.query<{ invited_email: string }>("SELECT invited_email FROM space_invitations WHERE id=$1 AND space_id=$2 AND revoked_at IS NULL AND consumed_at IS NULL FOR UPDATE", [id, spaceId])).rows[0];
        if (!previous) throw new InvitationError("invite_not_found");
        recipient = { id: null, name: null, email: previous.invited_email };
      } else recipient = { id: null, name: null, email: email! };
      const account = (await tx.query<{ id: string; name: string; email: string }>("SELECT id,name,email FROM users WHERE lower(email)=$1", [recipient.email.toLowerCase()])).rows[0];
      if (account) recipient = account;
      if ((await tx.query("SELECT 1 FROM space_members m JOIN users u ON u.id=m.user_id WHERE m.space_id=$1 AND lower(u.email)=lower($2)", [spaceId, recipient.email])).rowCount) throw new SpaceError("version_conflict");
      const tokenHash = hashToken(invitationToken(config.keys, config.keys.active, id, generation, recipient.email));
      if (existingId) await tx.query(`UPDATE space_invitations SET token_hash=$2,delivery_status='pending',expires_at=now()+interval '7 days',last_sent_at=NULL,invited_email=$3,invited_user_id=$4 WHERE id=$1`, [id, tokenHash, recipient.email, recipient.id]);
      else {
        await tx.query("UPDATE space_invitations SET revoked_at=now() WHERE space_id=$1 AND lower(invited_email)=lower($2) AND revoked_at IS NULL AND consumed_at IS NULL", [spaceId, recipient.email]);
        await tx.query(`INSERT INTO space_invitations(id,space_id,invited_user_id,invited_email,invited_by_user_id,token_hash,delivery_status,expires_at)
          VALUES($1,$2,$3,$4,$5,$6,'pending',now()+interval '7 days')`, [id, spaceId, recipient.id, recipient.email, userId, tokenHash]);
        await spaceEvent(tx, spaceId, userId, "member.invited", recipient.id ?? "", { invite_id: id });
      }
      await tx.query(`INSERT INTO space_invitation_delivery_jobs(invite_id,generation,token_key_id) VALUES($1,$2,$3)
        ON CONFLICT(invite_id) DO UPDATE SET generation=EXCLUDED.generation,token_key_id=EXCLUDED.token_key_id,state='pending',available_at=now(),attempts=0,
          lease_id=NULL,lease_expires_at=NULL,last_error='',updated_at=now()`, [id, generation, config.keys.active]);
      return read(tx, id);
    }, { mode: "service" }),
    revoke: (userId: string, spaceId: string, id: string) => withTransaction(pool, async (tx) => {
      await requireSpaceActor(tx, { userId }, spaceId, true);
      if (!(await tx.query("UPDATE space_invitations SET revoked_at=now() WHERE id=$1 AND space_id=$2 AND revoked_at IS NULL AND consumed_at IS NULL", [id, spaceId])).rowCount) throw new InvitationError("invite_not_found");
    }, { mode: "service" }),
    preview: (token: string) => withTransaction(pool, async (tx) => {
      const row = (await tx.query(`SELECT s.name AS space_name,u.name AS inviter_name,i.invited_email,i.expires_at FROM space_invitations i JOIN spaces s ON s.id=i.space_id
        JOIN users u ON u.id=i.invited_by_user_id WHERE i.token_hash=$1 AND i.revoked_at IS NULL AND i.consumed_at IS NULL AND i.expires_at>now()
        AND s.lifecycle_state='active' AND u.lifecycle_state='active'`, [hashToken(token)])).rows[0];
      if (!row) throw new InvitationError("invite_not_found"); return row;
    }, { mode: "service" }),
    respond: (userId: string, identity: { id: string } | { token: string }, accept: boolean) => withTransaction(pool, async (tx) => {
      const byToken = "token" in identity, value = byToken ? hashToken(identity.token) : identity.id;
      const candidate = (await tx.query<{ id: string; space_id: string }>(`SELECT id,space_id FROM space_invitations WHERE ${byToken ? "token_hash" : "id"}=$1`, [value])).rows[0];
      if (!candidate) throw new InvitationError("invite_not_found");
      // Resolve first, then lock Space/account before the invitation. Issuance,
      // revocation and acceptance therefore serialize in the same order.
      if (!(await tx.query("SELECT id FROM spaces WHERE id=$1 AND lifecycle_state='active' FOR UPDATE", [candidate.space_id])).rowCount) throw new InvitationError("invite_not_found");
      await requireActiveAccount(tx, userId);
      const current = (await tx.query<{ invited_email: string; expired: boolean; token_hash: string }>(`SELECT invited_email,expires_at<=now() AS expired,token_hash FROM space_invitations
        WHERE id=$1 AND revoked_at IS NULL AND consumed_at IS NULL FOR UPDATE`, [candidate.id])).rows[0];
      if (!current || byToken && current.token_hash !== value) throw new InvitationError("invite_not_found");
      const account = (await tx.query<{ email: string }>("SELECT email FROM users WHERE id=$1", [userId])).rows[0]!;
      if (current.invited_email.trim().toLowerCase() !== account.email.trim().toLowerCase()) throw new InvitationError("invite_not_found");
      if (current.expired) throw new InvitationError(byToken ? "invite_not_found" : "invite_expired");
      if (!accept) { await tx.query("UPDATE space_invitations SET revoked_at=now() WHERE id=$1", [candidate.id]); return null; }
      await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member') ON CONFLICT(space_id,user_id) DO NOTHING", [candidate.space_id, userId]);
      await tx.query("UPDATE space_invitations SET invited_user_id=$2,consumed_at=now() WHERE id=$1", [candidate.id, userId]);
      // Current notes belong to the Space; joining restores membership access
      // without reviving historical archived or deleted documents.
      await spaceEvent(tx, candidate.space_id, userId, "member.joined", userId, {});
      return readSpace(tx, userId, candidate.space_id);
    }, { mode: "service" }),
  };
}
