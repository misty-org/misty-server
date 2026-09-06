import { randomUUID } from "node:crypto";
import type { PoolClient } from "pg";
import { z } from "zod";
import { SpaceError, clientInteger } from "../spaces/model.js";
import { itemAudience } from "./model.js";
import { libraryAudit, lockLibraryItems } from "./mutation-model.js";
import { requireLibraryGrant } from "./reauthentication.js";
import { organizationString, organizationVersion, parseOrganization, type OrganizationTransaction } from "./organization-model.js";
const id = z.string().min(1), title = organizationString.refine(value => [...value].length <= 160);
const member = z.object({ item_id: id, role: z.enum(["still", "motion", "raw", "alternate", "burst_frame"]), position: z.number().int().min(0).max(2147483647).default(0) });
const input = z.object({ kind: organizationString.pipe(z.enum(["live_photo", "raw_pair", "burst"])), title, cover_item_id: id, motion_item_id: z.string().nullish().transform(value => value ?? ""), members: z.array(member).min(2).max(100) });
type StackRow = Record<string, unknown> & { id: string; kind: string; version: string };
async function readStacks(tx: PoolClient, userId: string, spaceId: string, stackId?: string) {
  const stacks = (await tx.query<StackRow>(`SELECT s.id,s.space_id,s.kind,s.title,s.cover_item_id,s.motion_item_id,s.effect,s.created_by_user_id,s.lifecycle_state,s.version,s.created_at,s.updated_at
    FROM space_library_asset_stacks s WHERE s.space_id=$1 AND s.lifecycle_state='ready'
    AND NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members m JOIN space_library_items i ON i.id=m.space_library_item_id WHERE m.stack_id=s.id AND
      (i.space_id<>$1 OR NOT ${itemAudience("i", "$2")}${stackId === undefined ? " OR i.lifecycle_state<>'ready' OR i.hidden" : ""}))
    ${stackId === undefined ? "ORDER BY s.created_at" : "AND s.id=$3"}`, stackId === undefined ? [spaceId, userId] : [spaceId, userId, stackId])).rows;
  const members = (await tx.query<{ stack_id: string; item_id: string; role: string; position: number; display_name: string; original_filename: string; mime_type: string }>(`SELECT m.stack_id,m.space_library_item_id AS item_id,m.role,m.position,i.display_name,f.original_filename,b.server_detected_mime_type AS mime_type
    FROM space_library_asset_stack_members m JOIN space_library_items i ON i.id=m.space_library_item_id JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
    WHERE m.stack_id=ANY($1::text[]) AND i.lifecycle_state='ready' AND i.hidden=FALSE ORDER BY m.position`, [stacks.map(row => row.id)])).rows;
  return stacks.map(row => {
    const value: Record<string, unknown> = { ...row, version: clientInteger(row.version), members: members.filter(m => m.stack_id === row.id).map(({ stack_id: _id, ...m }) => Object.fromEntries(Object.entries(m).filter(([, value]) => value !== ""))) };
    if (!value.motion_item_id) delete value.motion_item_id; return value;
  });
}
async function lockStack(tx: PoolClient, userId: string, spaceId: string, stackId: string, token: string) {
  const stack = (await tx.query<StackRow>("SELECT id,kind,version FROM space_library_asset_stacks WHERE id=$1 AND space_id=$2 AND lifecycle_state='ready' FOR UPDATE", [stackId, spaceId])).rows[0];
  if (!stack) throw new SpaceError("not_found");
  const members = (await tx.query<{ item_id: string; role: string; hidden: boolean; lifecycle_state: string; accessible: boolean }>(`SELECT i.id AS item_id,m.role,i.hidden,i.lifecycle_state,(i.space_id=$2 AND ${itemAudience("i", "$3")}) AS accessible FROM space_library_asset_stack_members m JOIN space_library_items i ON i.id=m.space_library_item_id WHERE m.stack_id=$1 ORDER BY i.id FOR SHARE OF i`, [stackId, spaceId, userId])).rows;
  if (!members.length || members.some(row => !row.accessible)) throw new SpaceError("not_found");
  await requireLibraryGrant(tx, userId, spaceId, members.some(row => row.lifecycle_state === "trash") ? "recently_deleted" : members.some(row => row.hidden) ? "hidden" : "", token);
  return { stack, members };
}
export function createAssetStacks(transaction: OrganizationTransaction) {
  return {
    list: (userId: string, spaceId: string) => transaction(userId, spaceId, false, async tx => ({ stacks: await readStacks(tx, userId, spaceId) })),
    create(userId: string, spaceId: string, token: string, raw: unknown) {
      const value = parseOrganization(input, raw), ids = value.members.map(m => m.item_id);
      if (new Set(ids).size !== ids.length || new Set(value.members.map(m => m.position)).size !== ids.length || !ids.includes(value.cover_item_id) || value.motion_item_id && !ids.includes(value.motion_item_id) || value.kind !== "burst" && ids.length !== 2) throw new SpaceError("invalid_request");
      return transaction(userId, spaceId, true, async tx => {
        const rows = await lockLibraryItems(tx, userId, spaceId, ids, token); if (rows.some(row => row.lifecycle_state !== "ready")) throw new SpaceError("not_found");
        const files = (await tx.query<{ id: string; mime: string; filename: string }>("SELECT i.id,b.server_detected_mime_type AS mime,f.original_filename AS filename FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.id=ANY($1::text[])", [ids])).rows;
        for (const m of value.members) {
          const file = files.find(f => f.id === m.item_id)!;
          const valid = value.kind === "burst" ? m.role === "burst_frame" && file.mime.startsWith("image/") : value.kind === "live_photo" ? m.role === "still" && file.mime.startsWith("image/") || m.role === "motion" && file.mime.startsWith("video/") : m.role === "raw" && /\.(dng|cr2|cr3|nef|nrw|arw|srf|sr2|raf|rw2|orf|pef|x3f)$/i.test(file.filename) || m.role === "alternate" && file.mime.startsWith("image/");
          if (!valid) throw new SpaceError("invalid_request");
        }
        const roles = value.members.map(m => m.role);
        if (value.kind === "live_photo" && (!roles.includes("still") || !roles.includes("motion") || value.members.find(m => m.item_id === value.motion_item_id)?.role !== "motion") || value.kind === "raw_pair" && (!roles.includes("raw") || !roles.includes("alternate")) || !["still", "alternate", "burst_frame"].includes(value.members.find(m => m.item_id === value.cover_item_id)!.role)) throw new SpaceError("invalid_request");
        await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`library:asset-stack:${spaceId}`]);
        if ((await tx.query("SELECT 1 FROM space_library_asset_stack_members m JOIN space_library_asset_stacks s ON s.id=m.stack_id WHERE s.space_id=$1 AND s.kind=$2 AND s.lifecycle_state='ready' AND m.space_library_item_id=ANY($3::text[]) LIMIT 1", [spaceId, value.kind, ids])).rowCount) throw new SpaceError("version_conflict");
        const stackId = `asset_stack_${randomUUID()}`;
        await tx.query("INSERT INTO space_library_asset_stacks(id,space_id,kind,title,cover_item_id,motion_item_id,created_by_user_id) VALUES($1,$2,$3,$4,$5,$6,$7)", [stackId, spaceId, value.kind, value.title, value.cover_item_id, value.motion_item_id || null, userId]);
        for (const m of value.members) await tx.query("INSERT INTO space_library_asset_stack_members(stack_id,space_library_item_id,role,position) VALUES($1,$2,$3,$4)", [stackId, m.item_id, m.role, m.position]);
        await libraryAudit(tx, userId, spaceId, "library.asset_stack.created", "asset_stack", stackId, { kind: value.kind, item_count: ids.length });
        return (await readStacks(tx, userId, spaceId, stackId))[0]!;
      });
    },
    update(userId: string, spaceId: string, stackId: string, token: string, raw: unknown) {
      const value = parseOrganization(z.object({ version: organizationVersion, title, cover_item_id: id, effect: organizationString.pipe(z.enum(["still", "loop", "bounce", "long_exposure"])) }), raw);
      return transaction(userId, spaceId, true, async tx => {
        const { stack, members } = await lockStack(tx, userId, spaceId, stackId, token);
        if (!members.some(m => m.item_id === value.cover_item_id && ["still", "alternate", "burst_frame"].includes(m.role)) || stack.kind !== "live_photo" && value.effect !== "still") throw new SpaceError("invalid_request");
        if (BigInt(stack.version) !== BigInt(value.version)) throw new SpaceError("version_conflict");
        await tx.query("UPDATE space_library_asset_stacks SET title=$2,cover_item_id=$3,effect=$4,version=version+1,updated_at=now() WHERE id=$1", [stackId, value.title, value.cover_item_id, value.effect]);
        await libraryAudit(tx, userId, spaceId, "library.asset_stack.updated", "asset_stack", stackId, { cover_item_id: value.cover_item_id, effect: value.effect });
        return (await readStacks(tx, userId, spaceId, stackId))[0]!;
      });
    },
    delete(userId: string, spaceId: string, stackId: string, token: string, version: number) {
      return transaction(userId, spaceId, true, async tx => {
        const { stack } = await lockStack(tx, userId, spaceId, stackId, token);
        if (BigInt(stack.version) !== BigInt(version)) throw new SpaceError("version_conflict");
        await tx.query("UPDATE space_library_asset_stacks SET lifecycle_state='deleted',version=version+1,updated_at=now() WHERE id=$1", [stackId]);
        await libraryAudit(tx, userId, spaceId, "library.asset_stack.deleted", "asset_stack", stackId, null);
      });
    },
  };
}
