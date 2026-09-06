import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { createCollaborationTickets } from "../collaboration/tickets.js";
import { integer, journalEvent, JournalError, requireJournalAudience, requireJournalMember, visibleAudience, type JournalActor } from "./access.js";

type Note = { id: string; space_id: string; creator_user_id: string; title_projection: string; markdown_projection: string; plain_text_projection: string;
  lifecycle_state: string; collaboration_revision: string; acl_version: string; audience_kind: string; audience_conversation_id: string | null;
  created_at: Date; updated_at: Date; backlink_count?: string };
const columns = "n.id,n.space_id,n.creator_user_id,n.title_projection,n.markdown_projection,n.plain_text_projection,n.lifecycle_state,n.collaboration_revision,n.acl_version,n.audience_kind,n.audience_conversation_id,n.created_at,n.updated_at";
const backlinkCount = `(SELECT count(*) FROM space_note_links links JOIN space_notes source ON source.id=links.source_note_id
  WHERE links.target_note_id=n.id AND source.space_id=n.space_id AND source.lifecycle_state='active' AND ${visibleAudience("source")}) AS backlink_count`;
function output(note: Note, userId: string, owner: boolean) {
  const { title_projection, markdown_projection, plain_text_projection, audience_conversation_id, collaboration_revision, acl_version, backlink_count, ...rest } = note;
  return { ...rest, title: title_projection, ...(markdown_projection ? { markdown: markdown_projection } : {}), ...(plain_text_projection ? { plain_text: plain_text_projection } : {}),
    ...(audience_conversation_id ? { audience_conversation_id } : {}), collaboration_revision: integer(collaboration_revision), acl_version: integer(acl_version),
    backlink_count: integer(backlink_count ?? "0"), role: note.creator_user_id === userId ? "creator" as const : "editor" as const, can_delete: note.creator_user_id === userId || owner };
}
async function selected(tx: PoolClient, actor: JournalActor, spaceId: string, id: string, write: boolean, includeInactive = false) {
  let member;
  try { member = await requireJournalMember(tx, actor, spaceId, write ? "notes.write" : "notes.read"); }
  catch (error) { if (error instanceof JournalError && error.code === "space_forbidden") throw new JournalError("not_found"); throw error; }
  const note = (await tx.query<Note>(`SELECT ${columns},${backlinkCount} FROM space_notes n WHERE n.id=$2 AND n.space_id=$3
    ${includeInactive ? "" : "AND n.lifecycle_state='active'"} FOR ${write ? "UPDATE" : "SHARE"} OF n`, [actor.userId, id, spaceId])).rows[0];
  if (!note) throw new JournalError("not_found");
  await requireJournalAudience(tx, actor, note);
  return { note, owner: member.owner };
}
async function control(tx: PoolClient, id: string, command: string, payload: object = {}) {
  await tx.query("INSERT INTO space_note_control_outbox(id,note_id,command,payload) VALUES($1,$2,$3,$4::jsonb)", [`notectl_${randomUUID()}`, id, command, JSON.stringify(payload)]);
}
export function createJournalNotes(pool: Pool, tickets: ReturnType<typeof createCollaborationTickets> | null) {
  return {
    list: (actor: JournalActor, spaceId: string) => withTransaction(pool, async (tx) => {
      const member = await requireJournalMember(tx, actor, spaceId, "notes.read");
      return (await tx.query<Note>(`SELECT ${columns},${backlinkCount} FROM space_notes n WHERE n.space_id=$2 AND n.lifecycle_state='active'
        AND ${visibleAudience("n")} ORDER BY n.updated_at DESC,n.id`, [actor.userId, spaceId])).rows.map((note) => output(note, actor.userId, member.owner));
    }, { mode: "service" }),
    get: (actor: JournalActor, spaceId: string, id: string) => withTransaction(pool, async (tx) => {
      const { note, owner } = await selected(tx, actor, spaceId, id, false); return output(note, actor.userId, owner);
    }, { mode: "service" }),
    create: (actor: JournalActor, spaceId: string, title: string) => withTransaction(pool, async (tx) => {
      const member = await requireJournalMember(tx, actor, spaceId, "notes.write"), id = `note_${randomUUID()}`;
      await tx.query("INSERT INTO space_notes(id,space_id,creator_user_id,title_projection,audience_kind) VALUES($1,$2,$3,$4,'space')", [id, spaceId, actor.userId, title]);
      await journalEvent(tx, spaceId, actor.userId, "note", "created", id);
      const note = (await tx.query<Note>(`SELECT ${columns} FROM space_notes n WHERE n.id=$1`, [id])).rows[0]!;
      return output(note, actor.userId, member.owner);
    }, { mode: "service" }),
    metadata: (actor: JournalActor, spaceId: string, id: string, tags: string[]) => withTransaction(pool, async (tx) => {
      if (tags.length > 50 || tags.some((tag) => !tag || Buffer.byteLength(tag, "utf8") > 80)) throw new JournalError("invalid_request");
      const { note, owner } = await selected(tx, actor, spaceId, id, true);
      const row = (await tx.query<{ updated_at: Date }>("UPDATE space_notes SET shared_tags=$2::jsonb,updated_at=now() WHERE id=$1 RETURNING updated_at", [id, JSON.stringify(tags)])).rows[0]!;
      await journalEvent(tx, spaceId, actor.userId, "note", "projection.updated", id);
      return output({ ...note, updated_at: row.updated_at }, actor.userId, owner);
    }, { mode: "service" }),
    archive: (actor: JournalActor, spaceId: string, id: string, archived: boolean) => withTransaction(pool, async (tx) => {
      const { note, owner } = await selected(tx, actor, spaceId, id, true, true);
      if (note.creator_user_id !== actor.userId && !owner || !["active", "archived"].includes(note.lifecycle_state)) throw new JournalError("not_found");
      const target = archived ? "archived" : "active";
      if (note.lifecycle_state === target) return;
      const changed = (await tx.query<{ acl_version: string }>(`UPDATE space_notes SET lifecycle_state=$2,
        archived_at=CASE WHEN $2='archived' THEN now() ELSE NULL END,purge_after=NULL,acl_version=acl_version+1,updated_at=now()
        WHERE id=$1 RETURNING acl_version`, [id, target])).rows[0]!;
      await control(tx, id, "acl", { acl_version: integer(changed.acl_version) });
      await journalEvent(tx, spaceId, actor.userId, "note", archived ? "archived" : "restored", id);
    }, { mode: "service" }),
    delete: (actor: JournalActor, spaceId: string, id: string) => withTransaction(pool, async (tx) => {
      const { note, owner } = await selected(tx, actor, spaceId, id, true, true);
      if (note.creator_user_id !== actor.userId && !owner) throw new JournalError("not_found");
      if (note.lifecycle_state === "deleting") return;
      if (!["active", "archived"].includes(note.lifecycle_state)) throw new JournalError("not_found");
      await tx.query("UPDATE space_notes SET lifecycle_state='deleting',acl_version=acl_version+1,updated_at=now() WHERE id=$1", [id]);
      await tx.query("UPDATE space_note_assets SET lifecycle_state='deleting',deleted_at=COALESCE(deleted_at,now()) WHERE note_id=$1 AND lifecycle_state IN ('ready','unreferenced')", [id]);
      await journalEvent(tx, spaceId, actor.userId, "note", "deleted", id);
      await control(tx, id, "purge");
    }, { mode: "service" }),
    backlinks: (actor: JournalActor, spaceId: string, id: string) => withTransaction(pool, async (tx) => {
      await selected(tx, actor, spaceId, id, false);
      return (await tx.query<{ id: string; title: string; updated_at: string }>(`SELECT source.id,source.title_projection AS title,source.updated_at::text AS updated_at
        FROM space_note_links links JOIN space_notes source ON source.id=links.source_note_id WHERE links.target_note_id=$2
        AND source.space_id=$3 AND source.lifecycle_state='active' AND ${visibleAudience("source")} ORDER BY source.updated_at DESC,source.id`, [actor.userId, id, spaceId])).rows;
    }, { mode: "service" }),
    ticket: (actor: JournalActor, spaceId: string, id: string) => withTransaction(pool, async (tx) => {
      const { note } = await selected(tx, actor, spaceId, id, true);
      if (!tickets) throw new JournalError("collaboration_unavailable");
      return tickets({ userId: actor.userId, spaceId, kind: "note", id, role: note.creator_user_id === actor.userId ? "creator" : "editor", aclVersion: integer(note.acl_version) });
    }, { mode: "service" }),
  };
}
