import type { PoolClient } from "pg";
import { SpaceError } from "../spaces/model.js";
import { transitionItems, type BulkOperation } from "./mutation-model.js";
export async function applyBulkAction(tx: PoolClient, userId: string, spaceId: string, operation: BulkOperation) {
  const ids = operation.items.map(item => item.id);
  const update = (set: string, extra: unknown[] = []) => tx.query(`UPDATE space_library_items i SET ${set},version=version+1,updated_at=now() WHERE i.space_id=$1 AND i.id=ANY($2::text[])`, [spaceId, ids, ...extra]);
  switch (operation.action) {
    case "favorite": case "unfavorite": await update("favorite=$3", [operation.action === "favorite"]); break;
    case "hide": case "unhide": await update("hidden=$3", [operation.action === "hide"]); break;
    case "trash": case "restore": await transitionItems(tx, spaceId, ids, operation.action === "restore"); break;
    case "add_to_album": case "remove_from_album": {
      if (!(await tx.query("SELECT id FROM space_albums WHERE id=$1 AND space_id=$2 FOR UPDATE", [operation.album_id, spaceId])).rowCount) throw new SpaceError("not_found");
      if (operation.action === "add_to_album") await tx.query("INSERT INTO space_album_items(album_id,space_library_item_id,added_by_user_id) SELECT $1,unnest($2::text[]),$3 ON CONFLICT DO NOTHING", [operation.album_id, ids, userId]);
      else await tx.query("DELETE FROM space_album_items WHERE album_id=$1 AND space_library_item_id=ANY($2::text[])", [operation.album_id, ids]);
      await tx.query("UPDATE space_albums SET version=version+1,updated_at=now() WHERE id=$1", [operation.album_id]); break;
    }
    case "add_tags": await update("tags=(SELECT COALESCE(jsonb_agg(value ORDER BY lower(value)),'[]'::jsonb) FROM (SELECT DISTINCT value FROM jsonb_array_elements_text(i.tags||$3::jsonb) value) tags)", [JSON.stringify(operation.tags)]); break;
    case "remove_tags": await update("tags=(SELECT COALESCE(jsonb_agg(value ORDER BY lower(value)),'[]'::jsonb) FROM jsonb_array_elements_text(i.tags) value WHERE lower(value)<>ALL($3::text[]))", [operation.tags.map(value => value.toLowerCase())]); break;
    case "set_date": await update("date_override=$3", [new Date(operation.date_override)]); break;
    case "clear_date": await update("date_override=NULL"); break;
    case "set_location": await update("location_override=$3::jsonb", [JSON.stringify(operation.location_override)]); break;
    case "clear_location": await update("location_override=NULL"); break;
  }
}
