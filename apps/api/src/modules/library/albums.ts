import { randomUUID } from "node:crypto";
import { z } from "zod";
import { SpaceError } from "../spaces/model.js";
import { libraryAudit } from "./mutation-model.js";
import { organizationString, organizationName, organizationVersion, organizationPosition, parseOrganization, readAlbums, readAlbum, requireVisibleItems, type OrganizationTransaction } from "./organization-model.js";
const metadata = z.object({ name: organizationName, description: organizationString.refine(value => [...value].length <= 2000) });
export function createAlbums(transaction: OrganizationTransaction) {
  return {
    list: (userId: string, spaceId: string) => transaction(userId, spaceId, false, async tx => ({ albums: await readAlbums(tx, userId, spaceId) })),
    get: (userId: string, spaceId: string, id: string) => transaction(userId, spaceId, false, tx => readAlbum(tx, userId, spaceId, id)),
    create(userId: string, spaceId: string, raw: unknown) {
      const value = parseOrganization(metadata, raw), id = `album_${randomUUID()}`;
      return transaction(userId, spaceId, true, async tx => {
        if (Number((await tx.query("SELECT count(*) AS count FROM space_albums WHERE space_id=$1", [spaceId])).rows[0].count) >= 500) throw new SpaceError("invalid_request");
        await tx.query("INSERT INTO space_albums(id,space_id,name,description,created_by_user_id) VALUES($1,$2,$3,$4,$5)", [id, spaceId, value.name, value.description, userId]);
        await libraryAudit(tx, userId, spaceId, "library.album.created", "album", id, {});
        return readAlbum(tx, userId, spaceId, id);
      });
    },
    update(userId: string, spaceId: string, id: string, raw: unknown) {
      const value = parseOrganization(metadata.extend({ version: organizationVersion, cover_item_id: organizationString }), raw);
      return transaction(userId, spaceId, true, async tx => {
        if (value.cover_item_id) {
          await requireVisibleItems(tx, userId, spaceId, [value.cover_item_id]);
          if (!(await tx.query("SELECT 1 FROM space_album_items WHERE album_id=$1 AND space_library_item_id=$2", [id, value.cover_item_id])).rowCount) throw new SpaceError("invalid_request");
        }
        if (!(await tx.query("UPDATE space_albums SET name=$3,description=$4,cover_item_id=$5,version=version+1,updated_at=now() WHERE space_id=$1 AND id=$2 AND version=$6", [spaceId, id, value.name, value.description, value.cover_item_id || null, value.version])).rowCount) throw new SpaceError("version_conflict");
        await libraryAudit(tx, userId, spaceId, "library.album.updated", "album", id, { cover_changed: true });
        return readAlbum(tx, userId, spaceId, id);
      });
    },
    delete(userId: string, spaceId: string, id: string, version: number) {
      return transaction(userId, spaceId, true, async tx => {
        if (!(await tx.query("DELETE FROM space_albums WHERE space_id=$1 AND id=$2 AND version=$3", [spaceId, id, version])).rowCount) throw new SpaceError("version_conflict");
        await libraryAudit(tx, userId, spaceId, "library.album.deleted", "album", id, {});
      });
    },
    organize(userId: string, spaceId: string, id: string, raw: unknown) {
      const value = parseOrganization(z.object({ version: organizationVersion, folder_id: organizationString, view_mode: organizationString.pipe(z.enum(["grid", "list"])), sort_mode: organizationString.pipe(z.enum(["custom", "oldest", "newest"])), position: organizationPosition }), raw);
      return transaction(userId, spaceId, true, async tx => {
        if (value.folder_id && !(await tx.query("SELECT id FROM space_album_folders WHERE space_id=$1 AND id=$2 FOR SHARE", [spaceId, value.folder_id])).rowCount) throw new SpaceError("invalid_request");
        if (!(await tx.query("UPDATE space_albums SET folder_id=$3,view_mode=$4,sort_mode=$5,position=$6,version=version+1,updated_at=now() WHERE space_id=$1 AND id=$2 AND version=$7", [spaceId, id, value.folder_id || null, value.view_mode, value.sort_mode, value.position, value.version])).rowCount) throw new SpaceError("version_conflict");
        await libraryAudit(tx, userId, spaceId, "library.album.organized", "album", id, { folder_id: value.folder_id, view_mode: value.view_mode, sort_mode: value.sort_mode, position: value.position });
        return readAlbum(tx, userId, spaceId, id);
      });
    },
  };
}
