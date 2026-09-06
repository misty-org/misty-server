import type { PoolClient } from "pg";
import { cancelSpaceRuns } from "../agents/cancellation.js";
import { revokeSpaceCollaboration } from "../journal/membership.js";
import { requireSpaceActor, type SpaceActor } from "./access.js";
import { SpaceError } from "./model.js";
import { spaceEvent } from "./templates.js";

export async function notifySpaceDeletion(tx: PoolClient, spaceId: string) {
  let after: string | null = null;
  const payload = (ids: string[]) => JSON.stringify({ type: "space.deleted", space_id: spaceId, user_ids: ids });
  // PostgreSQL NOTIFY has an 8 KiB payload limit. Page identities and split by
  // encoded bytes so a large Space does not make deletion fail at commit.
  for (;;) {
    const members: { user_id: string }[] = (await tx.query<{ user_id: string }>(`SELECT user_id FROM space_members
      WHERE space_id=$1 AND ($2::text IS NULL OR user_id>$2) ORDER BY user_id LIMIT 1000`, [spaceId, after])).rows;
    if (!members.length) return;
    let batch: string[] = [];
    for (const member of members) {
      if (Buffer.byteLength(payload([member.user_id])) > 7500) throw new Error("Space member identifier exceeds notification limit");
      if (Buffer.byteLength(payload([...batch, member.user_id])) > 7500) {
        await tx.query("SELECT pg_notify('misty_space_control',$1)", [payload(batch)]); batch = [];
      }
      batch.push(member.user_id);
    }
    if (batch.length) await tx.query("SELECT pg_notify('misty_space_control',$1)", [payload(batch)]);
    after = members.at(-1)!.user_id;
  }
}

export async function requestSpaceDeletion(tx: PoolClient, actor: SpaceActor, spaceId: string, confirmation: string) {
  await requireSpaceActor(tx, actor, spaceId, true);
  const space = (await tx.query<{ name: string; is_default: boolean }>("SELECT name,is_default FROM spaces WHERE id=$1", [spaceId])).rows[0]!;
  if (space.is_default) throw new SpaceError("default_space_protected");
  if (confirmation !== space.name) throw new SpaceError("invalid_request");
  await cancelSpaceRuns(tx, spaceId);
  await revokeSpaceCollaboration(tx, spaceId);
  await spaceEvent(tx, spaceId, actor.userId, "space.deletion_requested", spaceId, { recover_days: 30 });
  await tx.query(`UPDATE spaces SET lifecycle_state='pending_deletion',deletion_requested_at=now(),
    permanent_delete_after=now()+interval '30 days',updated_at=now() WHERE id=$1`, [spaceId]);
  await notifySpaceDeletion(tx, spaceId);
}
