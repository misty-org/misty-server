import { randomUUID } from "node:crypto";
import type { PoolClient } from "pg";
import { z } from "zod";
import { SpaceError, trimSpace } from "../spaces/model.js";
import { itemAudience, itemColumns, itemResponse, type ItemRow } from "./model.js";
import { requireLibraryGrant } from "./reauthentication.js";

const string = z.string().nullish().transform(value => value ?? "");
const boolean = z.boolean().nullish().transform(value => value ?? false);
const tags = z.array(string).nullish().transform(value => value ?? []);
export function normalizeTags(values: string[]) {
  const seen = new Set<string>();
  return values.map(trimSpace).filter(value => {
    const key = value.toLowerCase(); if (!value || [...value].length > 80 || seen.has(key)) return false;
    seen.add(key); return true;
  });
}
const updateSchema = z.object({ version: z.number().int().safe().nullish().transform(value => value ?? 0), display_name: string.transform(trimSpace), caption: string.transform(trimSpace), tags, favorite: boolean, hidden: boolean });
export function parseItemUpdate(raw: unknown) {
  const parsed = updateSchema.safeParse(raw);
  if (!parsed.success) throw new SpaceError("invalid_request");
  const value = parsed.data;
  if (!value.display_name || [...value.display_name].length > 255 || [...value.caption].length > 4000 || value.tags.length > 100) throw new SpaceError("invalid_request");
  return { ...value, tags: normalizeTags(value.tags) };
}
const actions = ["favorite", "unfavorite", "hide", "unhide", "trash", "restore", "add_to_album", "remove_from_album", "add_tags", "remove_tags", "set_date", "clear_date", "set_location", "clear_location"] as const;
const bulkSchema = z.object({ action: z.enum(actions), items: z.array(z.object({ id: z.string().refine(value => Boolean(trimSpace(value))), version: z.number().int().safe().min(1) })).min(1).max(200),
  album_id: string, tags, date_override: string, location_override: z.unknown().optional() });
export function parseBulkOperation(raw: unknown) {
  const parsed = bulkSchema.safeParse(raw); if (!parsed.success) throw new SpaceError("invalid_request");
  const value = parsed.data;
  if (new Set(value.items.map(item => item.id)).size !== value.items.length) throw new SpaceError("invalid_request");
  if (["add_to_album", "remove_from_album"].includes(value.action) && !value.album_id) throw new SpaceError("invalid_request");
  value.tags = normalizeTags(value.tags);
  if (["add_tags", "remove_tags"].includes(value.action) && (!value.tags.length || value.tags.length > 100)) throw new SpaceError("invalid_request");
  if (value.date_override && !z.iso.datetime({ offset: true }).safeParse(value.date_override).success || value.action === "set_date" && !value.date_override) throw new SpaceError("invalid_request");
  if (value.action === "set_location") {
    const location = value.location_override;
    if (!location || typeof location !== "object" || Array.isArray(location) || !Object.keys(location).length || Buffer.byteLength(JSON.stringify(location)) > 4096) throw new SpaceError("invalid_request");
  }
  return value;
}
export type BulkOperation = ReturnType<typeof parseBulkOperation>;
export async function lockLibraryItems(tx: PoolClient, userId: string, spaceId: string, ids: string[], token: string) {
  const rows = (await tx.query<{ id: string; version: string; lifecycle_state: string; hidden: boolean; recoverable: boolean }>(`
    SELECT i.id,i.version,i.lifecycle_state,i.hidden,i.recover_until>now() AS recoverable FROM space_library_items i
    WHERE i.space_id=$1 AND i.id=ANY($2::text[]) AND ${itemAudience("i", "$3")} ORDER BY i.id FOR UPDATE OF i`, [spaceId, ids, userId])).rows;
  if (rows.length !== ids.length) throw new SpaceError("not_found");
  for (const scope of new Set(rows.map(row => row.lifecycle_state === "trash" ? "recently_deleted" : row.hidden ? "hidden" : ""))) await requireLibraryGrant(tx, userId, spaceId, scope, token);
  return rows;
}
export async function readMutatedItems(tx: PoolClient, spaceId: string, ids: string[]) {
  return (await tx.query<ItemRow>(`SELECT ${itemColumns} FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.id=ANY($2::text[]) ORDER BY array_position($2::text[],i.id)`, [spaceId, ids])).rows.map(itemResponse);
}
export async function libraryAudit(tx: PoolClient, userId: string, spaceId: string, action: string, targetKind: string, targetId: string, details: unknown) {
  await tx.query("INSERT INTO space_library_audit_events(request_id,space_id,actor_user_id,action,target_kind,target_id,outcome,details) VALUES($1,$2,$3,$4,$5,$6,'success',$7::jsonb)", [`req_${randomUUID()}`, spaceId, userId, action, targetKind, targetId, JSON.stringify(details)]);
}
export async function transitionItems(tx: PoolClient, spaceId: string, ids: string[], restore: boolean) {
  await tx.query(`UPDATE space_library_items SET lifecycle_state=$3,trashed_at=${restore ? "NULL" : "now()"},recover_until=${restore ? "NULL" : "now()+interval '30 days'"},version=version+1,updated_at=now() WHERE space_id=$1 AND id=ANY($2::text[])`, [spaceId, ids, restore ? "ready" : "trash"]);
  await tx.query("UPDATE space_storage_contributions SET state=$3,updated_at=now() WHERE space_id=$1 AND source_kind='library_item' AND source_id=ANY($2::text[]) AND state=$4", [spaceId, ids, restore ? "active" : "recovery", restore ? "recovery" : "active"]);
}
