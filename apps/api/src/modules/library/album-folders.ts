import { randomUUID } from "node:crypto";
import { z } from "zod";
import { SpaceError } from "../spaces/model.js";
import { libraryAudit } from "./mutation-model.js";
import { organizationString, organizationName, organizationVersion, organizationPosition, parseOrganization, readFolders, readFolder, type OrganizationTransaction } from "./organization-model.js";
const metadata = z.object({ name: organizationName, parent_folder_id: organizationString });
export function createAlbumFolders(transaction: OrganizationTransaction) {
  return {
    list: (userId: string, spaceId: string) => transaction(userId, spaceId, false, async tx => ({ folders: await readFolders(tx, spaceId) })),
    create(userId: string, spaceId: string, raw: unknown) {
      const value = parseOrganization(metadata, raw), id = `album_folder_${randomUUID()}`;
      return transaction(userId, spaceId, true, async tx => {
        if (value.parent_folder_id && !(await tx.query("SELECT id FROM space_album_folders WHERE space_id=$1 AND id=$2", [spaceId, value.parent_folder_id])).rowCount) throw new SpaceError("invalid_request");
        await tx.query("INSERT INTO space_album_folders(id,space_id,parent_folder_id,name,position,created_by_user_id) VALUES($1,$2,$3,$4,(SELECT COALESCE(max(position),-1)+1 FROM space_album_folders WHERE space_id=$2 AND parent_folder_id IS NOT DISTINCT FROM $3),$5)", [id, spaceId, value.parent_folder_id || null, value.name, userId]);
        await libraryAudit(tx, userId, spaceId, "library.album_folder.created", "album_folder", id, { parent_folder_id: value.parent_folder_id });
        return readFolder(tx, spaceId, id);
      });
    },
    update(userId: string, spaceId: string, id: string, raw: unknown) {
      const value = parseOrganization(metadata.extend({ version: organizationVersion, position: organizationPosition }), raw);
      if (id === value.parent_folder_id) throw new SpaceError("invalid_request");
      return transaction(userId, spaceId, true, async tx => {
        if (value.parent_folder_id) {
          const row = (await tx.query<{ invalid: boolean }>(`WITH RECURSIVE descendants AS (
            SELECT id FROM space_album_folders WHERE space_id=$1 AND parent_folder_id=$2
            UNION SELECT child.id FROM space_album_folders child JOIN descendants d ON child.parent_folder_id=d.id WHERE child.space_id=$1)
            SELECT NOT EXISTS(SELECT 1 FROM space_album_folders WHERE id=$3 AND space_id=$1) OR EXISTS(SELECT 1 FROM descendants WHERE id=$3) AS invalid`, [spaceId, id, value.parent_folder_id])).rows[0]!;
          if (row.invalid) throw new SpaceError("invalid_request");
        }
        if (!(await tx.query("UPDATE space_album_folders SET parent_folder_id=$3,name=$4,position=$5,version=version+1,updated_at=now() WHERE space_id=$1 AND id=$2 AND version=$6", [spaceId, id, value.parent_folder_id || null, value.name, value.position, value.version])).rowCount) throw new SpaceError("version_conflict");
        await libraryAudit(tx, userId, spaceId, "library.album_folder.updated", "album_folder", id, {});
        return readFolder(tx, spaceId, id);
      });
    },
    delete(userId: string, spaceId: string, id: string, version: number) {
      return transaction(userId, spaceId, true, async tx => {
        if (!(await tx.query("DELETE FROM space_album_folders WHERE space_id=$1 AND id=$2 AND version=$3", [spaceId, id, version])).rowCount) throw new SpaceError("version_conflict");
        await libraryAudit(tx, userId, spaceId, "library.album_folder.deleted", "album_folder", id, {});
      });
    },
  };
}
