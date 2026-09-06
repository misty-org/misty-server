import type { PoolClient } from "pg";
import type { AppSession } from "../app-runtime/repository.js";
import { requireLiveAppSession } from "../app-runtime/repository.js";

export class JournalError extends Error {
  constructor(readonly code: "space_forbidden" | "not_found" | "invalid_request" | "collaboration_unavailable") { super(code); }
}
export type JournalActor = { userId: string; appSession?: AppSession };
/** Space before account (same order as usage/ownership); then app, member and document locks. */
export async function requireJournalMember(tx: PoolClient, actor: JournalActor, spaceId: string, scope: string) {
  const space = (await tx.query<{ owner_user_id: string }>("SELECT owner_user_id FROM spaces WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [spaceId])).rows[0];
  if (!space) throw new JournalError("space_forbidden");
  if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [actor.userId])).rowCount) throw new JournalError("space_forbidden");
  if (actor.appSession) {
    if (actor.appSession.space_id !== spaceId || actor.appSession.user_id !== actor.userId) throw new JournalError("space_forbidden");
    await requireLiveAppSession(tx, actor.appSession, scope);
  }
  const member = (await tx.query<{ role: string }>("SELECT role FROM space_members WHERE space_id=$1 AND user_id=$2 FOR SHARE", [spaceId, actor.userId])).rows[0];
  if (!member) throw new JournalError("space_forbidden");
  return { owner: member.role === "owner", ownerId: space.owner_user_id };
}
export async function requireJournalAudience(tx: PoolClient, actor: JournalActor, document: { space_id: string; audience_kind: string; audience_conversation_id: string | null }) {
  if (document.audience_kind === "space") return;
  if (document.audience_kind !== "conversation" || !(await tx.query(`SELECT cm.user_id FROM space_conversation_members cm
    JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=$1 AND cm.actor_kind='person'
      AND cm.user_id=$2 AND c.space_id=$3 FOR SHARE OF cm`, [document.audience_conversation_id, actor.userId, document.space_id])).rowCount) throw new JournalError("not_found");
}
/** Alias is always a repository-owned SQL identifier. */
export const visibleAudience = (alias: "n" | "d" | "source") => `(${alias}.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm
  JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=${alias}.audience_conversation_id
    AND cm.actor_kind='person' AND cm.user_id=$1 AND c.space_id=${alias}.space_id))`;
export async function journalEvent(tx: PoolClient, spaceId: string, userId: string, kind: "note" | "drawing", type: string, id: string) {
  const row = (await tx.query<{ id: string }>(`INSERT INTO space_events(space_id,event_type,actor_user_id,entity_id,payload)
    VALUES($1,$2,$3,$4,$5::jsonb) RETURNING id`, [spaceId, `${kind}.${type}`, userId, id, JSON.stringify({ [`${kind}_id`]: id })])).rows[0]!;
  await tx.query("SELECT pg_notify('misty_space_events',$1)", [row.id]);
}
export function integer(value: string | number) {
  const parsed = Number(value); if (!Number.isSafeInteger(parsed)) throw new Error("Journal integer exceeds client precision"); return parsed;
}
