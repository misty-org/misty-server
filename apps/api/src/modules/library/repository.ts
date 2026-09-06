import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { SpaceError, clientInteger, trimSpace } from "../spaces/model.js";
import { lockStorage } from "../storage/quota.js";
import { personalStorageSummary } from "../storage/summary.js";
import { spaceStorageSummary } from "../storage/space-summary.js";
import { coverOnly, itemAudience, itemColumns, itemResponse, requireLibraryPermission, type ItemRow } from "./model.js";
import { listFilter, mediaSubtype, subtypes } from "./filters.js";
import { libraryQuery } from "./search.js";
import { requireLibraryGrant } from "./reauthentication.js";
import { facetSql } from "./facets-sql.js";

export function createLibraryRepository(pool: Pool) {
  const read = <T>(userId: string, spaceId: string, operation: (tx: PoolClient) => Promise<T>) => withTransaction(pool, async tx => {
    await tx.query("SET LOCAL statement_timeout='5s'"); await tx.query("SET LOCAL lock_timeout='2s'");
    await requireLibraryPermission(tx, userId, spaceId); return operation(tx);
  }, { mode: "service" }, { isolationLevel: "repeatable read" });
  return {
    list(userId: string, spaceId: string, rawQuery: Record<string, string>, token: string) {
      const query = libraryQuery(rawQuery), filter = listFilter(spaceId, userId, query);
      return read(userId, spaceId, async tx => {
        // Check effective structured filters as well as explicit query parameters.
        await requireLibraryGrant(tx, userId, spaceId, query.state === "trash" ? "recently_deleted" : query.visibility !== "visible" ? "hidden" : "", token);
        const items = (await tx.query<ItemRow>(`SELECT ${itemColumns} FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id ${filter.sql}`, filter.args)).rows.map(itemResponse);
        return { items, next_after: items.length === query.limit ? items.at(-1)!.id : "" };
      });
    },
    get(userId: string, spaceId: string, id: string, token: string) {
      return read(userId, spaceId, async tx => {
        const row = (await tx.query<ItemRow>(`SELECT ${itemColumns} FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.id=$1 AND i.space_id=$2 AND ${itemAudience("i", "$3")}`, [id, spaceId, userId])).rows[0];
        if (!row) throw new SpaceError("not_found");
        await requireLibraryGrant(tx, userId, spaceId, row.lifecycle_state === "trash" ? "recently_deleted" : row.hidden ? "hidden" : "", token);
        return itemResponse(row);
      });
    },
    facets(userId: string, spaceId: string, rawPrefix: string) {
      const prefix = trimSpace(rawPrefix); if ([...prefix].length > 120) throw new SpaceError("invalid_request");
      return read(userId, spaceId, async tx => {
        const visible = (position: number) => `WITH accessible_items AS (SELECT i.* FROM space_library_items i WHERE i.space_id=$1 AND ${itemAudience("i", `$${position}`)}) `;
        const counts = (await tx.query<Record<string, string>>(`${visible(2)}SELECT * FROM (${facetSql.counts}) counts(total,favorites,hidden,recently_deleted)`, [spaceId, userId])).rows[0]!;
        const result = { total: clientInteger(counts.total!), favorites: clientInteger(counts.favorites!), hidden: clientInteger(counts.hidden!), recently_deleted: clientInteger(counts.recently_deleted!),
          tags: [] as Facet[], media_types: [] as Facet[], years: [] as Facet[], albums: [] as Facet[], utilities: [] as Facet[] };
        for (const key of ["tags", "media_types", "years", "albums", "utilities"] as const) {
          const args = key === "utilities" ? [spaceId, userId, prefix, `%${prefix}%`, userId] : [spaceId, prefix, `%${prefix}%`, userId];
          result[key] = (await tx.query<Omit<Facet, "count"> & { count: string }>(`${visible(args.length)}SELECT * FROM (${facetSql[key]}) facets(value,label,count)`, args)).rows.map(row => ({ ...row, count: clientInteger(row.count) }));
        }
        for (const [value, label] of subtypes) {
          const row = (await tx.query<{ count: string }>(`${visible(2)}SELECT count(*) AS count FROM accessible_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.lifecycle_state='ready' AND i.hidden=FALSE AND ${coverOnly} AND ${mediaSubtype(value)}`, [spaceId, userId])).rows[0]!;
          if (BigInt(row.count)) result.media_types.push({ value, label, count: clientInteger(row.count) });
        }
        return result;
      });
    },
    usage(userId: string, spaceId: string) {
      return withTransaction(pool, async tx => {
        await requireLibraryPermission(tx, userId, spaceId, "storage.view_own_usage");
        const ownerId = (await tx.query<{ owner_user_id: string }>("SELECT owner_user_id FROM spaces WHERE id=$1", [spaceId])).rows[0]!.owner_user_id;
        await lockStorage(tx, userId, spaceId);
        const personal = await personalStorageSummary(tx, userId), summary = await spaceStorageSummary(tx, userId, spaceId, ownerId, personal.personal);
        return userId === ownerId ? summary : { space_id: spaceId, storage_available: summary.personal_remaining_bytes > 0 && summary.space_remaining_bytes > 0 };
      }, { mode: "service" });
    },
  };
}
type Facet = { value: string; label: string; count: number };
