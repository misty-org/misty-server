import type { PoolClient } from "pg";
import { requireSpaceActor, type SpaceActor } from "../../spaces/access.js";
import { spacePermissions } from "../../spaces/permissions.js";
import { SpaceError } from "../../spaces/model.js";
import { requireLiveAppSession } from "../../app-runtime/repository.js";

export type CalendarSourceAction = "list" | "manage" | "available" | "sync";
export async function requireCalendarSourceActor(tx: PoolClient, actor: SpaceActor, spaceId: string, action: CalendarSourceAction) {
  const member = await requireSpaceActor(tx, actor, spaceId, false, action === "list" || action === "available" ? "calendar.read" : "calendar.write");
  if (actor.appSession && action === "available") await requireLiveAppSession(tx, actor.appSession, "connections.read");
  if (actor.appSession && action === "sync") await requireLiveAppSession(tx, actor.appSession, "tasks.write");
  const permissions: Record<string, boolean> = await spacePermissions(tx, actor.userId, { id: spaceId, role: member.role, is_default: false });
  if (!permissions[action === "manage" || action === "available" ? "integrations.manage" : "tasks.view"]) throw new SpaceError("forbidden");
}
