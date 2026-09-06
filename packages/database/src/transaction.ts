import type { Pool, PoolClient } from "pg";

/** Only trusted repositories construct these values, never request JSON. */
export type RlsIdentity =
  | { mode: "anonymous" | "waitlist"; email: string }
  | { mode: "session"; sessionHash: string; userId?: string }
  | { mode: "user"; userId: string; email?: string; licenseId?: string }
  | { mode: "registration"; userId: string; email: string; licenseId: string }
  | { mode: "service" };

export async function withTransaction<T>(
  pool: Pick<Pool, "connect">,
  operation: (client: PoolClient) => Promise<T>,
  identity?: RlsIdentity,
  options?: { isolationLevel: "repeatable read" },
): Promise<T> {
  const client = await pool.connect();
  let discard = false, connectionError: Error | undefined;
  // pg emits connection failures even while an operation awaits a remote
  // provider. Keep a listener until release, and never commit after lock loss.
  const failed = (error: Error) => { discard = true; connectionError = error; };
  client.on("error", failed);
  try {
    await client.query(options?.isolationLevel === "repeatable read" ? "BEGIN ISOLATION LEVEL REPEATABLE READ" : "BEGIN");
    if (identity) {
      const settings: Record<string, string> = { "app.rls_mode": identity.mode };
      if ("userId" in identity && identity.userId !== undefined) settings["app.current_user_id"] = identity.userId;
      if ("sessionHash" in identity) settings["app.current_session_token_hash"] = identity.sessionHash;
      if ("email" in identity && identity.email !== undefined) settings["app.current_email"] = identity.email.trim().toLowerCase();
      if ("licenseId" in identity && identity.licenseId !== undefined) settings["app.current_license_id"] = identity.licenseId;
      for (const [key, value] of Object.entries(settings)) {
        await client.query("SELECT set_config($1, $2, true)", [key, value]);
      }
    }
    const result = await operation(client);
    if (connectionError) throw connectionError;
    await client.query("COMMIT");
    return result;
  } catch (error) {
    try {
      await client.query("ROLLBACK");
    } catch {
      discard = true;
    }
    throw error;
  } finally {
    client.release(discard);
    client.off("error", failed);
  }
}
