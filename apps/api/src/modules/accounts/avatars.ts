import { createHash, randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { IdentifierSchema } from "@misty/contracts";
import { requireSpaceActor, type SpaceActor } from "../spaces/access.js";
import { SpaceError } from "../spaces/model.js";
import { StorageUnavailable, type ByteObjectStore } from "../storage/object-store.js";
import { AccountUnavailable } from "./repository.js";
import { AvatarError, avatarMaxBytes, avatarPngConfig } from "./avatar-png.js";

type AccountActor = { userId: string; sessionHash: string };
type Reader = AccountActor | { actor: SpaceActor; spaceId: string; memberId: string; sessionHash?: string };
export function createAvatarService(options: { pool: Pool; store: ByteObjectStore | null; deployment: "hosted" | "self_hosted" }) {
  const transaction = <T>(operation: (tx: PoolClient) => Promise<T>) => withTransaction(options.pool, operation, { mode: "service" });
  const selfHost = async (tx: PoolClient, userId: string) => {
    if (options.deployment === "self_hosted" && !(await tx.query("SELECT user_id FROM self_host_accounts WHERE user_id=$1 AND disabled_at IS NULL AND entitlement_expires_at>now() FOR SHARE", [userId])).rowCount) throw new AccountUnavailable();
  };
  const account = async (tx: PoolClient, actor: AccountActor, write = false) => {
    if (!(await tx.query(`SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR ${write ? "UPDATE" : "SHARE"}`, [actor.userId])).rowCount ||
      !(await tx.query("SELECT token_hash FROM sessions WHERE user_id=$1 AND token_hash=$2 AND expires_at>now() FOR SHARE", [actor.userId, actor.sessionHash])).rowCount) throw new AccountUnavailable();
    await selfHost(tx, actor.userId);
    return actor.userId;
  };
  const reader = async (tx: PoolClient, input: Reader) => {
    if (!("actor" in input)) return account(tx, input);
    const spaceId = IdentifierSchema.safeParse(input.spaceId), memberId = IdentifierSchema.safeParse(input.memberId);
    if (!spaceId.success || !memberId.success) throw new SpaceError("invalid_request");
    await requireSpaceActor(tx, input.actor, spaceId.data, false, "spaces.read");
    if (!input.actor.appSession) await account(tx, { userId: input.actor.userId, sessionHash: input.sessionHash ?? "" });
    else await selfHost(tx, input.actor.userId);
    if (!(await tx.query(`SELECT m.user_id FROM space_members m JOIN users u ON u.id=m.user_id
      WHERE m.space_id=$1 AND m.user_id=$2 AND u.lifecycle_state='active' FOR SHARE OF m`, [spaceId.data, memberId.data])).rowCount) throw new SpaceError("forbidden");
    return memberId.data;
  };
  return {
    async upload(actor: AccountActor, bytes: Buffer, requestSignal: AbortSignal) {
      avatarPngConfig(bytes);
      const store = options.store; if (!store) throw new StorageUnavailable();
      const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(20000)]), key = `avatars/avatar_${randomUUID()}`;
      signal.throwIfAborted();
      // Publish no pointer until PUT succeeds. The durable intent survives a crash
      // or failed commit and bounds unpublished/replaced objects for this owner.
      await transaction(async (tx) => {
        await account(tx, actor, true);
        if ((await tx.query("SELECT 1 FROM object_deletion_jobs WHERE created_by_user_id=$1 LIMIT 12", [actor.userId])).rowCount === 12) throw new AvatarError("upload_limit");
        await tx.query("INSERT INTO object_deletion_jobs(object_key,not_before,created_by_user_id) VALUES($1,now()+interval '15 minutes',$2)", [key, actor.userId]);
      });
      await store.putBytes(key, bytes, { byteSize: bytes.length, mimeType: "image/png", sha256: createHash("sha256").update(bytes).digest("hex") }, signal);
      signal.throwIfAborted();
      return transaction(async (tx) => {
        await account(tx, actor, true);
        const pending = await tx.query("SELECT object_key FROM object_deletion_jobs WHERE object_key=$1 AND created_by_user_id=$2 AND not_before>now() AND lease_id IS NULL FOR UPDATE", [key, actor.userId]);
        if (!pending.rowCount) throw new AvatarError("upload_expired");
        signal.throwIfAborted();
        const row = (await tx.query<{ version: number }>(`UPDATE users SET avatar_object_key=$2,avatar_version=avatar_version+1,avatar_updated_at=now()
          WHERE id=$1 AND avatar_version<9007199254740991 RETURNING avatar_version::float8 AS version`, [actor.userId, key])).rows[0];
        if (!row) throw new Error("Avatar version exhausted");
        await tx.query("DELETE FROM object_deletion_jobs WHERE object_key=$1", [key]);
        return { avatar_version: row.version };
      });
    },
    async read(input: Reader, requestSignal: AbortSignal) {
      const metadata = await transaction(async (tx) => {
        const userId = await reader(tx, input);
        const row = (await tx.query<{ version: string; key: string }>(`SELECT avatar_version::text AS version,COALESCE(avatar_object_key,'avatars/'||id) AS key
          FROM users WHERE id=$1 AND lifecycle_state='active' AND avatar_version>0`, [userId])).rows[0];
        if (!row) throw new AvatarError("not_found"); return row;
      });
      if (!options.store) throw new AvatarError("not_found");
      const data = await options.store.getBytes(metadata.key, avatarMaxBytes, requestSignal);
      if (!data) throw new AvatarError("not_found");
      // An immutable object's bytes and version stay consistent across replacement.
      // Recheck access after transfer so membership/session revocation wins.
      await transaction((tx) => reader(tx, input));
      requestSignal.throwIfAborted();
      return { data, version: metadata.version };
    },
  };
}
export type AvatarService = ReturnType<typeof createAvatarService>;
