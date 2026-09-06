import type { Pool, PoolClient } from "pg";
import { z } from "zod";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { SpaceError, clientInteger, trimSpace } from "../spaces/model.js";
import { itemAudience, requireLibraryPermission } from "./model.js";
export const organizationString = z.string().nullish().transform(value => trimSpace(value ?? ""));
export const organizationVersion = z.number().int().safe().min(1);
export const organizationPosition = z.number().int().safe().min(0).nullish().transform(value => value ?? 0);
export const organizationName = organizationString.refine(value => Boolean(value) && [...value].length <= 120);
export function parseOrganization<T>(schema: z.ZodType<T>, value: unknown): T {
  const parsed = schema.safeParse(value); if (!parsed.success) throw new SpaceError("invalid_request"); return parsed.data;
}
export function queryVersion(raw: string | undefined) {
  return parseOrganization(organizationVersion, /^[+-]?\d+$/.test(raw ?? "") ? Number(raw) : 0);
}
export function organizationTransactions(pool: Pool) {
  return async <T>(userId: string, spaceId: string, write: boolean, operation: (tx: PoolClient) => Promise<T>) => {
    try { return await withTransaction(pool, async tx => {
      await tx.query("SET LOCAL statement_timeout='5s'"); await tx.query("SET LOCAL lock_timeout='2s'");
      await requireLibraryPermission(tx, userId, spaceId);
      if (write) {
        await requireLibraryPermission(tx, userId, spaceId, "library.edit");
        // Serialize native folder ancestry changes and album limit checks.
        await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`library-organization:${spaceId}`]);
      }
      return operation(tx);
    }, { mode: "service" }); } catch (error) {
      if (error && typeof error === "object" && "code" in error && error.code === "23505") throw new SpaceError("version_conflict");
      throw error;
    }
  };
}
export type OrganizationTransaction = ReturnType<typeof organizationTransactions>;
const visible = (alias: string) => `${alias}.space_id=$1 AND ${alias}.lifecycle_state='ready' AND ${alias}.hidden=FALSE AND ${itemAudience(alias, "$2")}`;
const albumSelect = `SELECT a.id,a.space_id,a.folder_id,a.name,a.description,
  (SELECT cover.id FROM space_library_items cover WHERE cover.id=a.cover_item_id AND ${visible("cover")}) AS cover_item_id,
  a.position,a.view_mode,a.sort_mode,a.created_by_user_id,
  (SELECT count(*) FROM space_album_items ai JOIN space_library_items i ON i.id=ai.space_library_item_id WHERE ai.album_id=a.id AND ${visible("i")}) AS item_count,
  a.version,a.created_at,a.updated_at FROM space_albums a WHERE a.space_id=$1`;
type OrganizationRow = Record<string, unknown> & { id: string; version: string; position: string };
function response(row: OrganizationRow, counts: string[], nullable: string[]) {
  const result: Record<string, unknown> = { ...row, version: clientInteger(row.version), position: clientInteger(row.position) };
  for (const key of counts) result[key] = clientInteger(row[key] as string);
  for (const key of nullable) if (!result[key]) delete result[key];
  return result;
}
export async function readAlbums(tx: PoolClient, userId: string, spaceId: string, id?: string) {
  return (await tx.query<OrganizationRow>(`${albumSelect}${id === undefined ? " ORDER BY a.position,lower(a.name),a.id" : " AND a.id=$3"}`, id === undefined ? [spaceId, userId] : [spaceId, userId, id])).rows.map(row => response(row, ["item_count"], ["folder_id", "cover_item_id"]));
}
export async function readAlbum(tx: PoolClient, userId: string, spaceId: string, id: string) {
  const album = (await readAlbums(tx, userId, spaceId, id))[0]; if (!album) throw new SpaceError("not_found"); return album;
}
export async function readFolders(tx: PoolClient, spaceId: string, id?: string) {
  return (await tx.query<OrganizationRow>(`SELECT f.id,f.space_id,f.parent_folder_id,f.name,f.position,f.created_by_user_id,f.version,f.created_at,f.updated_at,
    (SELECT count(*) FROM space_albums a WHERE a.folder_id=f.id AND a.space_id=$1) AS album_count,
    (SELECT count(*) FROM space_album_folders child WHERE child.parent_folder_id=f.id AND child.space_id=$1) AS folder_count
    FROM space_album_folders f WHERE f.space_id=$1${id === undefined ? " ORDER BY f.position,lower(f.name),f.id" : " AND f.id=$2"}`, id === undefined ? [spaceId] : [spaceId, id])).rows.map(row => response(row, ["album_count", "folder_count"], ["parent_folder_id"]));
}
export async function readFolder(tx: PoolClient, spaceId: string, id: string) { const folder = (await readFolders(tx, spaceId, id))[0]; if (!folder) throw new SpaceError("not_found"); return folder; }
export async function requireVisibleItems(tx: PoolClient, userId: string, spaceId: string, ids: string[]) {
  const count = (await tx.query(`SELECT i.id FROM space_library_items i WHERE ${visible("i")} AND i.id=ANY($3::text[]) ORDER BY i.id FOR SHARE OF i`, [spaceId, userId, ids])).rowCount;
  if (count !== ids.length) throw new SpaceError("invalid_request");
}
export async function lockAlbum(tx: PoolClient, spaceId: string, albumId: string, version?: number) {
  const row = (await tx.query<{ version: string }>("SELECT version FROM space_albums WHERE space_id=$1 AND id=$2 FOR UPDATE", [spaceId, albumId])).rows[0];
  if (!row) throw new SpaceError("not_found");
  if (version !== undefined && BigInt(row.version) !== BigInt(version)) throw new SpaceError("version_conflict");
}
