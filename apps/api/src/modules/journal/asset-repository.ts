import { randomBytes, randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { hashToken } from "../auth/service.js";
import { lockStorage, checkStorageCapacity } from "../storage/quota.js";
import { requireJournalMember, requireJournalAudience, JournalError, integer, journalEvent, type JournalActor } from "./access.js";
import type { ObjectMetadata } from "../storage/object-store.js";
import { queueObjectDeletion as queueDeletion } from "../storage/jobs.js";

export type AssetKind = "note" | "drawing";
export type AssetInput = { filename: string; mime_type: string; byte_size: number; sha256: string; file_id?: string };
export class AssetError extends Error {
  constructor(readonly code: "forbidden" | "conflict" | "upload_mismatch") { super(code); }
}
type Upload = { id: string; space_id: string; security_domain_id: string; user_id: string; object_key: string; original_filename: string;
  purpose: string; client_declared_mime_type: string; requested_byte_size: string; client_sha256: string; state: string; upload_token_hash: string;
  expires_at: Date; file_id: string | null; note_id: string | null; drawing_id: string | null; drawing_file_id: string | null };
const domain = (kind: AssetKind) => kind === "note" ? { documents: "space_notes", assets: "space_note_assets", parent: "note_id", purpose: "note_attachment", prefix: "noteasset" }
  : { documents: "space_drawings", assets: "space_drawing_assets", parent: "drawing_id", purpose: "drawing_attachment", prefix: "drawingasset" };
async function authorize(tx: PoolClient, actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string, write = true) {
  const member = await requireJournalMember(tx, actor, spaceId, `${kind}s.${write ? "write" : "read"}`);
  const doc = (await tx.query<{ space_id: string; creator_user_id: string; audience_kind: string; audience_conversation_id: string | null }>(
    `SELECT space_id,creator_user_id,audience_kind,audience_conversation_id FROM ${domain(kind).documents} WHERE id=$1 AND space_id=$2 AND lifecycle_state='active' FOR SHARE`, [parentId, spaceId])).rows[0];
  if (!doc) throw new JournalError("not_found");
  await requireJournalAudience(tx, actor, doc);
  return { ...member, creatorId: doc.creator_user_id };
}
async function audit(tx: PoolClient, upload: Upload, action: string, targetKind: string, targetId: string, details: object, outcome = "success") {
  await tx.query(`INSERT INTO space_library_audit_events(request_id,security_domain_id,space_id,actor_user_id,action,target_kind,target_id,outcome,details)
    VALUES($1,$2,$3,$4,$5,$6,$7,$9,$8::jsonb)`, [`req_${randomUUID()}`, upload.security_domain_id, upload.space_id, upload.user_id, action, targetKind, targetId, JSON.stringify(details), outcome]);
}
async function publicUpload(tx: PoolClient, uploadId: string) {
  const row = (await tx.query(`SELECT id,space_id,security_domain_id,user_id,original_filename,purpose,client_declared_mime_type,
    requested_byte_size,client_sha256,verified_byte_size,verified_sha256,detected_mime_type,state,file_id,error_code,expires_at,version,created_at,updated_at
    FROM space_library_uploads WHERE id=$1`, [uploadId])).rows[0]!;
  for (const key of Object.keys(row)) if (row[key] === null) delete row[key];
  return { ...row, requested_byte_size: integer(row.requested_byte_size), version: integer(row.version),
    ...(row.verified_byte_size !== undefined ? { verified_byte_size: integer(row.verified_byte_size) } : {}) };
}
async function completed(tx: PoolClient, kind: AssetKind, uploadId: string, fileId: string) {
  const file = (await tx.query(`SELECT id,blob_id,security_domain_id,uploader_user_id,original_filename,intrinsic_metadata,lifecycle_state,original_uploaded_at,version
    FROM library_files WHERE id=$1`, [fileId])).rows[0];
  if (!file) throw new JournalError("not_found");
  return { upload: await publicUpload(tx, uploadId), file: { ...file, version: integer(file.version) }, [`${kind}_asset`]: await assetRecord(tx, kind, fileId) };
}
async function assetRecord(tx: PoolClient, kind: AssetKind, fileId: string) {
  const d = domain(kind);
  const row = (await tx.query(`SELECT a.*,COALESCE(NULLIF(b.server_detected_mime_type,''),b.client_declared_mime_type) AS mime_type,b.byte_size,b.sha256
    FROM ${d.assets} a JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
    WHERE a.file_id=$1 AND a.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'`, [fileId])).rows[0];
  if (!row) throw new JournalError("not_found");
  return { ...row, byte_size: integer(row.byte_size) };
}
export function createAssetRepository(pool: Pool) {
  const tx = <T>(run: (client: PoolClient) => Promise<T>) => withTransaction(pool, run, { mode: "service" });
  async function selected(client: PoolClient, actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string, uploadId: string, token: string) {
    await authorize(client, actor, spaceId, kind, parentId);
    await lockStorage(client, actor.userId, spaceId);
    const upload = (await client.query<Upload>("SELECT * FROM space_library_uploads WHERE id=$1 AND space_id=$2 AND user_id=$3 FOR UPDATE", [uploadId, spaceId, actor.userId])).rows[0];
    if (!upload || upload[domain(kind).parent as "note_id" | "drawing_id"] !== parentId || upload.purpose !== domain(kind).purpose) throw new JournalError("not_found");
    if (!token || upload.upload_token_hash !== hashToken(token)) throw new AssetError("forbidden");
    const now = (await client.query<{ now: Date }>("SELECT now() AS now")).rows[0]!.now;
    if (upload.expires_at <= now) throw new AssetError("forbidden");
    if (!["initiated", "uploaded_unverified", "ready"].includes(upload.state)) throw new AssetError("conflict");
    return upload;
  }
  return {
    async list(actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string) {
      return tx(async (client) => {
        await authorize(client, actor, spaceId, kind, parentId, false);
        const rows = (await client.query(`SELECT a.*,COALESCE(NULLIF(b.server_detected_mime_type,''),b.client_declared_mime_type) AS mime_type,b.byte_size,b.sha256
          FROM ${domain(kind).assets} a JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
          WHERE a.${domain(kind).parent}=$1 AND a.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready' ORDER BY a.created_at,a.id`, [parentId])).rows;
        return rows.map((row) => ({ ...row, byte_size: integer(row.byte_size) }));
      });
    },
    async remove(actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string, assetId: string) {
      return tx(async (client) => {
        const member = await authorize(client, actor, spaceId, kind, parentId);
        const state = kind === "note" && (member.owner || member.creatorId === actor.userId) ? "deleting" : "unreferenced";
        const changed = await client.query(`UPDATE ${domain(kind).assets} SET lifecycle_state=$1,deleted_at=now()
          WHERE id=$2 AND ${domain(kind).parent}=$3 AND lifecycle_state='ready'`, [state, assetId, parentId]);
        if (changed.rowCount && kind === "note") await journalEvent(client, spaceId, actor.userId, kind, "projection.updated", parentId);
      });
    },
    async reserve(actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string, input: AssetInput) {
      const token = randomBytes(32).toString("base64url"), id = `upload_${randomUUID()}`, key = `library/${randomUUID().replaceAll("-", "")}`;
      const upload = await tx(async (client) => {
        const member = await authorize(client, actor, spaceId, kind, parentId);
        const usage = await lockStorage(client, actor.userId, spaceId);
        await checkStorageCapacity(client, actor.userId, spaceId, member.ownerId, usage, BigInt(input.byte_size));
        const row = (await client.query<Upload>(`INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,
          client_declared_mime_type,requested_byte_size,client_sha256,state,upload_token_hash,expires_at,note_id,drawing_id,drawing_file_id)
          SELECT $1,s.id,s.security_domain_id,$3,$4,$5,$6,$7,$8,$9,'initiated',$10,now()+interval '30 minutes',$11,$12,$13 FROM spaces s WHERE s.id=$2 RETURNING *`,
        [id, spaceId, actor.userId, key, input.filename, domain(kind).purpose, input.mime_type, input.byte_size, input.sha256, hashToken(token),
          kind === "note" ? parentId : null, kind === "drawing" ? parentId : null, input.file_id ?? null])).rows[0]!;
        await client.query(`INSERT INTO space_upload_reservations(upload_id,space_id,user_id,reserved_bytes,state,expires_at) VALUES($1,$2,$3,$4,'active',$5)`, [id, spaceId, actor.userId, input.byte_size, row.expires_at]);
        await client.query("UPDATE space_storage_usage SET reserved_bytes=reserved_bytes+$1,version=version+1,updated_at=now() WHERE space_id=$2", [input.byte_size, spaceId]);
        await audit(client, row, `${kind}.asset.upload.initiated`, "upload", id, { [`${kind}_id`]: parentId, reserved_bytes: input.byte_size });
        return { internal: row, public: await publicUpload(client, id) };
      });
      return { upload: upload.internal, publicUpload: upload.public, token };
    },
    inspect: (actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string, uploadId: string, token: string) =>
      tx((client) => selected(client, actor, spaceId, kind, parentId, uploadId, token)),
    async complete(actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string, uploadId: string, token: string, verified: Map<string, ObjectMetadata | null>) {
      return tx(async (client): Promise<{ needHead: string } | { result: Record<string, unknown> } | { mismatch: true }> => {
        const upload = await selected(client, actor, spaceId, kind, parentId, uploadId, token), size = BigInt(upload.requested_byte_size);
        if (upload.state === "ready") return { result: await completed(client, kind, upload.id, upload.file_id!) };
        if (!verified.has(upload.object_key)) return { needHead: upload.object_key };
        const proof = verified.get(upload.object_key);
        const reservation = (await client.query<{ reserved_bytes: string }>("SELECT reserved_bytes FROM space_upload_reservations WHERE upload_id=$1 AND state='active' FOR UPDATE", [upload.id])).rows[0];
        if (!reservation || BigInt(reservation.reserved_bytes) !== size) throw new AssetError("conflict");
        if (!proof || BigInt(proof.byteSize) !== size || proof.sha256 !== upload.client_sha256 || proof.mimeType !== upload.client_declared_mime_type) {
          await client.query("UPDATE space_upload_reservations SET state='released',updated_at=now() WHERE upload_id=$1", [upload.id]);
          const changed = await client.query("UPDATE space_storage_usage SET reserved_bytes=reserved_bytes-$1,version=version+1,updated_at=now() WHERE space_id=$2 AND reserved_bytes >= $1", [size, spaceId]);
          if (!changed.rowCount) throw new Error("Storage reservation accounting mismatch");
          await client.query("UPDATE space_library_uploads SET state='invalid',error_code='object_missing_or_mismatched',version=version+1,updated_at=now() WHERE id=$1", [upload.id]);
          await queueDeletion(client, upload.object_key, upload.expires_at);
          await audit(client, upload, "library.upload.invalid", "upload", upload.id, { error_code: "object_missing_or_mismatched", released_bytes: integer(upload.requested_byte_size) }, "failed");
          const event = (await client.query<{ id: string }>(`INSERT INTO space_events(space_id,event_type,actor_user_id,entity_id,payload)
            VALUES($1,'library.upload.invalid',$2,$3,$4::jsonb) RETURNING id`, [spaceId, actor.userId, upload.id,
            JSON.stringify({ upload_id: upload.id, state: "invalid", error_code: "object_missing_or_mismatched" })])).rows[0]!;
          await client.query("SELECT pg_notify('misty_space_events',$1)", [event.id]);
          return { mismatch: true };
        }
        await client.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`library:blob:${upload.security_domain_id}:${proof.sha256}${proof.byteSize}`]);
        let blob = (await client.query<{ id: string; r2_object_key: string; lifecycle_state: string }>(`SELECT id,r2_object_key,lifecycle_state FROM library_blobs
          WHERE security_domain_id=$1 AND sha256=$2 AND byte_size=$3 AND lifecycle_state<>'deleted' FOR UPDATE`, [upload.security_domain_id, proof.sha256, size])).rows[0];
        if (blob && blob.lifecycle_state !== "ready") throw new AssetError("conflict");
        if (blob && blob.r2_object_key !== upload.object_key && !verified.has(blob.r2_object_key)) return { needHead: blob.r2_object_key };
        if (blob && blob.r2_object_key !== upload.object_key) {
          const existing = verified.get(blob.r2_object_key);
          if (!existing || existing.sha256 !== proof.sha256 || existing.byteSize !== proof.byteSize) {
            await queueDeletion(client, blob.r2_object_key, upload.expires_at);
            await client.query("UPDATE library_blobs SET r2_object_key=$1,version=version+1,updated_at=now() WHERE id=$2", [upload.object_key, blob.id]);
            blob.r2_object_key = upload.object_key;
          } else await queueDeletion(client, upload.object_key, upload.expires_at);
        }
        if (!blob) {
          blob = { id: `blob_${randomUUID()}`, r2_object_key: upload.object_key, lifecycle_state: "ready" };
          await client.query(`INSERT INTO library_blobs(id,security_domain_id,r2_object_key,sha256,byte_size,client_declared_mime_type,server_detected_mime_type,scan_status,processing_status,lifecycle_state)
            VALUES($1,$2,$3,$4,$5,$6,$6,'skipped','ready','ready')`, [blob.id, upload.security_domain_id, upload.object_key, proof.sha256, size, proof.mimeType]);
        }
        const fileId = `file_${randomUUID()}`, assetId = `${domain(kind).prefix}_${randomUUID()}`;
        await client.query(`INSERT INTO library_files(id,blob_id,security_domain_id,uploader_user_id,original_filename,intrinsic_metadata,lifecycle_state)
          VALUES($1,$2,$3,$4,$5,$6::jsonb,'ready')`, [fileId, blob.id, upload.security_domain_id, actor.userId, upload.original_filename,
          JSON.stringify({ byte_size: proof.byteSize, sha256: proof.sha256, client_declared_mime_type: proof.mimeType })]);
        await client.query(`INSERT INTO ${domain(kind).assets}(id,${domain(kind).parent},file_id,uploader_user_id,display_name${kind === "drawing" ? ",excalidraw_file_id" : ""})
          VALUES($1,$2,$3,$4,$5${kind === "drawing" ? ",$6" : ""})`, [assetId, parentId, fileId, actor.userId, upload.original_filename, ...(kind === "drawing" ? [upload.drawing_file_id] : [])]);
        await client.query(`INSERT INTO space_storage_contributions(id,space_id,user_id,file_id,source_kind,source_id,logical_bytes,state) VALUES($1,$2,$3,$4,$5,$6,$7,'active')`,
        [`contribution_${randomUUID()}`, spaceId, actor.userId, fileId, `${kind}_asset`, assetId, size]);
        await client.query("UPDATE space_upload_reservations SET state='consumed',updated_at=now() WHERE upload_id=$1", [upload.id]);
        const changed = await client.query("UPDATE space_storage_usage SET reserved_bytes=reserved_bytes-$1,used_bytes=used_bytes+$1,version=version+1,updated_at=now() WHERE space_id=$2 AND reserved_bytes >= $1", [size, spaceId]);
        if (!changed.rowCount) throw new Error("Storage reservation accounting mismatch");
        await client.query(`UPDATE space_library_uploads SET state='ready',file_id=$2,verified_byte_size=$3,verified_sha256=$4,detected_mime_type=$5,finalized_at=now(),version=version+1,updated_at=now() WHERE id=$1`,
        [upload.id, fileId, size, proof.sha256, proof.mimeType]);
        const event = (await client.query<{ id: string }>(`INSERT INTO space_events(space_id,event_type,actor_user_id,entity_id,payload)
          VALUES($1,'library.upload.ready',$2,$3,$4::jsonb) RETURNING id`, [spaceId, actor.userId, upload.id,
          JSON.stringify({ upload_id: upload.id, state: "ready", item_id: assetId, purpose: upload.purpose })])).rows[0]!;
        await client.query("SELECT pg_notify('misty_space_events',$1)", [event.id]);
        await audit(client, upload, "library.upload.ready", `${kind}_asset`, assetId, { logical_bytes: proof.byteSize, deduplicated: blob.r2_object_key !== upload.object_key });
        return { result: await completed(client, kind, upload.id, fileId) };
      });
    },
    async download(actor: JournalActor, spaceId: string, kind: AssetKind, parentId: string, assetId: string) {
      return tx(async (client) => {
        await authorize(client, actor, spaceId, kind, parentId, false);
        const row = (await client.query<{ object_key: string; filename: string; mime_type: string; byte_size: string; sha256: string }>(`SELECT b.r2_object_key AS object_key,a.display_name AS filename,
          COALESCE(NULLIF(b.server_detected_mime_type,''),b.client_declared_mime_type) AS mime_type,b.byte_size,b.sha256
          FROM ${domain(kind).assets} a JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
          WHERE a.id=$1 AND a.${domain(kind).parent}=$2 AND a.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'`, [assetId, parentId])).rows[0];
        if (!row) throw new JournalError("not_found");
        return { ...row, byte_size: integer(row.byte_size) };
      });
    },
  };
}
