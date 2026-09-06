import type { PoolClient } from "pg";
import { requireSpaceActor } from "../spaces/access.js";
import { spacePermissions } from "../spaces/permissions.js";
import { clientInteger, SpaceError } from "../spaces/model.js";
export class LibraryReauthenticationRequired extends Error {}
export class LibraryReauthenticationFailed extends Error {}
export async function requireLibraryPermission(tx: PoolClient, userId: string, spaceId: string, permission = "library.view") {
  const member = await requireSpaceActor(tx, { userId }, spaceId);
  const permissions: Record<string, boolean> = await spacePermissions(tx, userId, { id: spaceId, role: member.role, is_default: false });
  if (!permissions[permission]) throw new SpaceError("forbidden");
}
export const itemAudience = (alias: string, user: string) => `(${alias}.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id
  WHERE cm.conversation_id=${alias}.audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=${user} AND c.space_id=${alias}.space_id))`;
export const coverOnly = "NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members sm JOIN space_library_asset_stacks s ON s.id=sm.stack_id WHERE sm.space_library_item_id=i.id AND s.lifecycle_state='ready' AND s.cover_item_id<>i.id)";
export const itemColumns = `i.id,i.space_id,i.file_id,i.contributing_user_id,i.display_name,i.caption,i.tags,i.favorite,i.hidden,i.date_override,i.location_override,i.contributor_information,i.current_edit_version_id,
  i.added_by_user_id,i.lifecycle_state,i.added_at,i.trashed_at,i.recover_until,i.version,i.updated_at,
  jsonb_build_object('id',f.id,'blob_id',f.blob_id,'security_domain_id',f.security_domain_id,'uploader_user_id',f.uploader_user_id,'original_filename',f.original_filename,
    'intrinsic_metadata',f.intrinsic_metadata,'lifecycle_state',f.lifecycle_state,'original_uploaded_at',f.original_uploaded_at,'version',f.version::text) AS file`;
export type ItemRow = Record<string, unknown> & { id: string; hidden: boolean; lifecycle_state: string; version: string; file: Record<string, unknown> & { version: string } };
export function itemResponse(row: ItemRow) {
  const result: Record<string, unknown> = { ...row, version: clientInteger(row.version), file: { ...row.file, version: clientInteger(row.file.version) } };
  for (const key of ["date_override", "current_edit_version_id", "trashed_at", "recover_until"]) if (result[key] === null || result[key] === "") delete result[key];
  return result;
}
