import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { createCollaborationTickets } from "../collaboration/tickets.js";
import { integer, journalEvent, JournalError, requireJournalAudience, requireJournalMember, visibleAudience, type JournalActor } from "./access.js";

type Drawing = { id: string; space_id: string; creator_user_id: string; title: string; lifecycle_state: string; collaboration_revision: string; acl_version: string;
  audience_kind: string; audience_conversation_id: string | null; created_at: Date; updated_at: Date };
const columns = "d.id,d.space_id,d.creator_user_id,d.title,d.lifecycle_state,d.collaboration_revision,d.acl_version,d.audience_kind,d.audience_conversation_id,d.created_at,d.updated_at";
function output(drawing: Drawing, userId: string, owner: boolean) {
  const { audience_conversation_id, collaboration_revision, acl_version, ...rest } = drawing;
  return { ...rest, ...(audience_conversation_id ? { audience_conversation_id } : {}), collaboration_revision: integer(collaboration_revision), acl_version: integer(acl_version),
    role: drawing.creator_user_id === userId ? "creator" as const : "editor" as const, can_delete: drawing.creator_user_id === userId || owner };
}
function title(value: string) {
  const normalized = value.trim() || "Untitled drawing";
  if ([...normalized].length > 200) throw new JournalError("invalid_request");
  return normalized;
}
async function selected(tx: PoolClient, actor: JournalActor, spaceId: string, id: string, write: boolean) {
  let member;
  try { member = await requireJournalMember(tx, actor, spaceId, write ? "drawings.write" : "drawings.read"); }
  catch (error) { if (error instanceof JournalError && error.code === "space_forbidden") throw new JournalError("not_found"); throw error; }
  const drawing = (await tx.query<Drawing>(`SELECT ${columns} FROM space_drawings d WHERE d.id=$1 AND d.space_id=$2 AND d.lifecycle_state='active'
    FOR ${write ? "UPDATE" : "SHARE"} OF d`, [id, spaceId])).rows[0];
  if (!drawing) throw new JournalError("not_found");
  await requireJournalAudience(tx, actor, drawing);
  return { drawing, owner: member.owner };
}
export function createJournalDrawings(pool: Pool, tickets: ReturnType<typeof createCollaborationTickets> | null) {
  return {
    list: (actor: JournalActor, spaceId: string) => withTransaction(pool, async (tx) => {
      const member = await requireJournalMember(tx, actor, spaceId, "drawings.read");
      return (await tx.query<Drawing>(`SELECT ${columns} FROM space_drawings d WHERE d.space_id=$2 AND d.lifecycle_state='active'
        AND ${visibleAudience("d")} ORDER BY d.updated_at DESC,d.id`, [actor.userId, spaceId])).rows.map((drawing) => output(drawing, actor.userId, member.owner));
    }, { mode: "service" }),
    get: (actor: JournalActor, spaceId: string, id: string) => withTransaction(pool, async (tx) => {
      const { drawing, owner } = await selected(tx, actor, spaceId, id, false); return output(drawing, actor.userId, owner);
    }, { mode: "service" }),
    create: (actor: JournalActor, spaceId: string, value: string) => withTransaction(pool, async (tx) => {
      const normalized = title(value), member = await requireJournalMember(tx, actor, spaceId, "drawings.write"), id = `drawing_${randomUUID()}`;
      await tx.query("INSERT INTO space_drawings(id,space_id,creator_user_id,title,audience_kind) VALUES($1,$2,$3,$4,'space')", [id, spaceId, actor.userId, normalized]);
      await journalEvent(tx, spaceId, actor.userId, "drawing", "created", id);
      const drawing = (await tx.query<Drawing>(`SELECT ${columns} FROM space_drawings d WHERE d.id=$1`, [id])).rows[0]!;
      return output(drawing, actor.userId, member.owner);
    }, { mode: "service" }),
    rename: (actor: JournalActor, spaceId: string, id: string, value: string) => withTransaction(pool, async (tx) => {
      const normalized = title(value), { drawing, owner } = await selected(tx, actor, spaceId, id, true);
      const row = (await tx.query<{ updated_at: Date }>("UPDATE space_drawings SET title=$2,updated_at=now() WHERE id=$1 RETURNING updated_at", [id, normalized])).rows[0]!;
      await journalEvent(tx, spaceId, actor.userId, "drawing", "updated", id);
      return output({ ...drawing, title: normalized, updated_at: row.updated_at }, actor.userId, owner);
    }, { mode: "service" }),
    delete: (actor: JournalActor, spaceId: string, id: string) => withTransaction(pool, async (tx) => {
      const { drawing, owner } = await selected(tx, actor, spaceId, id, true);
      if (drawing.creator_user_id !== actor.userId && !owner) throw new JournalError("not_found");
      await tx.query("UPDATE space_drawings SET lifecycle_state='deleting',acl_version=acl_version+1,updated_at=now() WHERE id=$1", [id]);
      await tx.query("UPDATE space_drawing_assets SET lifecycle_state='deleting',deleted_at=COALESCE(deleted_at,now()) WHERE drawing_id=$1 AND lifecycle_state IN ('ready','unreferenced')", [id]);
      await journalEvent(tx, spaceId, actor.userId, "drawing", "deleted", id);
      await tx.query("INSERT INTO space_drawing_control_outbox(id,drawing_id,command,payload) VALUES($1,$2,'purge','{}')", [`drawingctl_${randomUUID()}`, id]);
    }, { mode: "service" }),
    ticket: (actor: JournalActor, spaceId: string, id: string) => withTransaction(pool, async (tx) => {
      const { drawing } = await selected(tx, actor, spaceId, id, true);
      if (!tickets) throw new JournalError("collaboration_unavailable");
      return tickets({ userId: actor.userId, spaceId, kind: "drawing", id, role: drawing.creator_user_id === actor.userId ? "creator" : "editor", aclVersion: integer(drawing.acl_version) });
    }, { mode: "service" }),
  };
}
