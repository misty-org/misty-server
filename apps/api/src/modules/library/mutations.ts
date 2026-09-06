import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { SpaceError, clientInteger } from "../spaces/model.js";
import { requireLibraryPermission } from "./model.js";
import { requireLibraryGrant } from "./reauthentication.js";
import { parseItemUpdate, parseBulkOperation, lockLibraryItems, readMutatedItems, libraryAudit, transitionItems } from "./mutation-model.js";
import { applyBulkAction } from "./bulk-actions.js";
export function createLibraryMutations(pool: Pool) {
  const write = <T>(userId: string, spaceId: string, operation: (tx: PoolClient) => Promise<T>) => withTransaction(pool, async tx => {
    await tx.query("SET LOCAL statement_timeout='5s'"); await tx.query("SET LOCAL lock_timeout='2s'");
    await requireLibraryPermission(tx, userId, spaceId);
    await requireLibraryPermission(tx, userId, spaceId, "library.edit");
    return operation(tx);
  }, { mode: "service" });
  return {
    update(userId: string, spaceId: string, itemId: string, token: string, raw: unknown) {
      const value = parseItemUpdate(raw);
      return write(userId, spaceId, async tx => {
        const row = (await lockLibraryItems(tx, userId, spaceId, [itemId], token))[0]!;
        if (BigInt(row.version) !== BigInt(value.version) || row.lifecycle_state !== "ready") throw new SpaceError("version_conflict");
        await tx.query("UPDATE space_library_items SET display_name=$3,caption=$4,tags=$5::jsonb,favorite=$6,hidden=$7,version=version+1,updated_at=now() WHERE space_id=$1 AND id=$2", [spaceId, itemId, value.display_name, value.caption, JSON.stringify(value.tags), value.favorite, value.hidden]);
        await libraryAudit(tx, userId, spaceId, "library.item.updated", "library_item", itemId, { version: clientInteger(BigInt(row.version) + 1n) });
        return (await readMutatedItems(tx, spaceId, [itemId]))[0]!;
      });
    },
    transition(userId: string, spaceId: string, itemId: string, token: string, restore: boolean) {
      return write(userId, spaceId, async tx => {
        if (restore) await requireLibraryGrant(tx, userId, spaceId, "recently_deleted", token);
        const row = (await lockLibraryItems(tx, userId, spaceId, [itemId], token))[0]!;
        if (row.lifecycle_state !== (restore ? "trash" : "ready") || restore && !row.recoverable) throw new SpaceError("not_found");
        await transitionItems(tx, spaceId, [itemId], restore);
        await libraryAudit(tx, userId, spaceId, restore ? "library.item.restored" : "library.item.trashed", "library_item", itemId, {});
        return (await readMutatedItems(tx, spaceId, [itemId]))[0]!;
      });
    },
    bulk(userId: string, spaceId: string, token: string, raw: unknown) {
      const operation = parseBulkOperation(raw), ids = operation.items.map(item => item.id);
      return write(userId, spaceId, async tx => {
        const rows = await lockLibraryItems(tx, userId, spaceId, ids, token), restore = operation.action === "restore";
        if (restore) await requireLibraryGrant(tx, userId, spaceId, "recently_deleted", token);
        const versions = new Map(operation.items.map(item => [item.id, BigInt(item.version)]));
        for (const row of rows) {
          if (BigInt(row.version) !== versions.get(row.id) || row.lifecycle_state !== (restore ? "trash" : "ready")) throw new SpaceError("version_conflict");
          if (restore && !row.recoverable) throw new SpaceError("not_found");
        }
        await applyBulkAction(tx, userId, spaceId, operation);
        await libraryAudit(tx, userId, spaceId, `library.items.bulk.${operation.action}`, "library_items", "", { count: ids.length, album_id: operation.album_id });
        if (operation.action === "trash" || restore) for (const id of ids) await libraryAudit(tx, userId, spaceId, restore ? "library.item.restored" : "library.item.trashed", "library_item", id, { bulk: true });
        return { items: await readMutatedItems(tx, spaceId, ids) };
      });
    },
  };
}
