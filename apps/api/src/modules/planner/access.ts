import type { PoolClient } from "pg";
import { requireSpaceActor, type SpaceActor } from "../spaces/access.js";
import { spacePermissions } from "../spaces/permissions.js";
import { SpaceError } from "../spaces/model.js";

export async function requirePlannerActor(tx: PoolClient, actor: SpaceActor, spaceId: string, appScope: "tasks.read" | "tasks.write" | "calendar.read" | "calendar.write" | "roadmaps.read" | "roadmaps.write", write = false) {
  const member = await requireSpaceActor(tx, actor, spaceId, false, appScope);
  const permissions: Record<string, boolean> = await spacePermissions(tx, actor.userId, { id: spaceId, role: member.role, is_default: false });
  if (!permissions[write ? "tasks.manage" : "tasks.view"]) throw new SpaceError("forbidden");
  return permissions;
}
// Repository-owned alias and placeholder only. Validate the conversation's
// Space as well as membership, including inconsistent historical rows.
export const taskAudience = (userParameter: string) => `(t.audience_kind='space' OR (t.audience_kind='conversation' AND EXISTS(
  SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id
  WHERE cm.conversation_id=t.audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=${userParameter} AND c.space_id=t.space_id)))`;
