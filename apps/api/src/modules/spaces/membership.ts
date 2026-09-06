import type { PoolClient } from "pg";
import { cancelMemberRuns } from "../agents/cancellation.js";
import { revokeSpaceCollaboration } from "../journal/membership.js";
import { requireSpaceActor, type SpaceActor } from "./access.js";
import { SpaceError } from "./model.js";
import { spaceEvent } from "./templates.js";

export async function removeMembership(tx: PoolClient, actor: SpaceActor, spaceId: string, targetId?: string) {
  if (actor.appSession) throw new SpaceError("forbidden");
  // Lock before the account/member checks, including leave. Upgrading a shared
  // Space lock after authorization would deadlock concurrent membership changes.
  if (!(await tx.query("SELECT id FROM spaces WHERE id=$1 AND lifecycle_state='active' FOR UPDATE", [spaceId])).rowCount) throw new SpaceError("forbidden");
  const member = await requireSpaceActor(tx, actor, spaceId, targetId !== undefined);
  const userId = targetId ?? actor.userId;
  if (targetId === actor.userId || targetId === undefined && member.role === "owner") throw new SpaceError("invalid_request");
  if (!(await tx.query("DELETE FROM space_members WHERE space_id=$1 AND user_id=$2 AND role='member'", [spaceId, userId])).rowCount) throw new SpaceError("not_found");
  await tx.query(`DELETE FROM space_conversation_members cm USING space_conversations c
    WHERE cm.conversation_id=c.id AND c.space_id=$1 AND cm.user_id=$2`, [spaceId, userId]);
  await cancelMemberRuns(tx, spaceId, userId);
  await revokeSpaceCollaboration(tx, spaceId);
  const type = targetId === undefined ? "member.left" : "member.removed";
  await spaceEvent(tx, spaceId, actor.userId, type, userId, {});
  await tx.query("SELECT pg_notify('misty_space_control',$1)", [JSON.stringify({ type, space_id: spaceId, user_ids: [userId] })]);
}
