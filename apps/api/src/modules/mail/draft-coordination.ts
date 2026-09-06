import { createHash } from "node:crypto";
import type { Pool } from "pg";
import { MailError } from "./errors.js";

/** One reserved connection owns the session lock and every SQL transaction in
 * the operation. Nested repository calls must use the supplied borrowed pool. */
export async function withDraftConnection<T>(pool: Pick<Pool, "connect">, connectionId: string, draftId: string,
  requestSignal: AbortSignal, operation: (pool: Pick<Pool, "connect">, signal: AbortSignal) => Promise<T>): Promise<T> {
  const client = await pool.connect(), controller = new AbortController();
  const signal = AbortSignal.any([requestSignal, controller.signal]);
  const key = draftId ? createHash("sha256").update(JSON.stringify(["misty-mail-draft-v1", connectionId, draftId])).digest().readBigInt64BE().toString() : null;
  let locked = false, discard = false;
  const lost = () => { discard = true; controller.abort(new MailError("mail_provider_unavailable")); };
  client.on("error", lost); client.on("end", lost);
  // Bind pg methods to their actual client. withTransaction may release its
  // borrowed handle, but only this owner can return the real client to the pool.
  const borrowed = new Proxy(client, { get(target, property) {
    if (property === "release") return (failed?: Error | boolean) => { if (failed) lost(); };
    const value: unknown = Reflect.get(target, property, target);
    return typeof value === "function" ? value.bind(target) : value;
  } });
  try {
    signal.throwIfAborted();
    if (key) {
      locked = (await client.query<{ locked: boolean }>("SELECT pg_try_advisory_lock($1::bigint) AS locked", [key])).rows[0]!.locked;
      if (!locked) throw new MailError("mail_draft_busy");
    }
    const result = await operation({ connect: async () => borrowed }, signal);
    signal.throwIfAborted(); return result;
  } finally {
    // Never reuse a connection that might still own a session advisory lock.
    if (locked && !discard) try {
      if (!(await client.query<{ unlocked: boolean }>("SELECT pg_advisory_unlock($1::bigint) AS unlocked", [key])).rows[0]?.unlocked) discard = true;
    } catch { discard = true; }
    client.removeListener("error", lost); client.removeListener("end", lost);
    client.release(discard); controller.abort();
  }
}
