import { z } from "zod";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";

const sessionSchema = z.object({
  token_hash: z.string().length(64),
  user_id: z.string().min(1), app_id: z.string().min(1), space_id: z.string(),
  scopes: z.array(z.string()), expires_at: z.date(),
});
export type AppSession = z.infer<typeof sessionSchema>;
export class AppSessionRevoked extends Error {}

export async function requireLiveAppSession(tx: PoolClient, session: AppSession, scope?: string) {
  if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [session.user_id])).rowCount) throw new AppSessionRevoked();
  const installation = (await tx.query<{ granted_scopes: string[] }>(`SELECT granted_scopes FROM user_app_installations
    WHERE user_id=$1 AND app_id=$2 AND state='installed' FOR SHARE`, [session.user_id, session.app_id])).rows[0];
  if (!installation) throw new AppSessionRevoked();
  // Keep the credential and installation locked through the data operation.
  // Uninstall, grant changes and account deletion cannot race a stale request
  // into recreating private data after its installation has been purged.
  const live = await tx.query(`SELECT token_hash FROM app_runtime_sessions WHERE token_hash=$1 AND user_id=$2 AND app_id=$3
    AND expires_at>now() AND scopes=$4::jsonb AND scopes<@$5::jsonb AND COALESCE(space_id,'')=$6
    AND ($7::text IS NULL OR scopes ? $7) FOR SHARE`, [session.token_hash, session.user_id, session.app_id,
    JSON.stringify(session.scopes), JSON.stringify(installation.granted_scopes), session.space_id, scope ?? null]);
  if (!live.rowCount) throw new AppSessionRevoked();
}
export interface AppRecord {
  key: string;
  data: unknown;
  created_at: Date;
  updated_at: Date;
}
export interface AppRuntimeRepository {
  findSession(tokenHash: string): Promise<AppSession | null>;
  isSpaceMember(session: AppSession): Promise<boolean>;
  listRecords(session: AppSession): Promise<AppRecord[]>;
  putRecord(session: AppSession, key: string, data: unknown): Promise<AppRecord>;
  deleteRecord(session: AppSession, key: string): Promise<boolean>;
}

export function createAppRuntimeRepository(pool: Pool): AppRuntimeRepository {
  return {
    findSession: (tokenHash) => withTransaction(pool, async (tx) => {
      const result = await tx.query(`SELECT s.token_hash, s.user_id, s.app_id, COALESCE(s.space_id, '') AS space_id, s.scopes, s.expires_at
        FROM app_runtime_sessions s
        JOIN user_app_installations i ON i.user_id=s.user_id AND i.app_id=s.app_id
        JOIN users u ON u.id=s.user_id
        WHERE s.token_hash=$1 AND s.expires_at>NOW() AND i.state='installed'
          AND s.scopes <@ i.granted_scopes AND u.lifecycle_state='active'`, [tokenHash]);
      return result.rows[0] ? sessionSchema.parse(result.rows[0]) : null;
    }, { mode: "service" }),
    isSpaceMember: (session) => withTransaction(pool, async (tx) => {
      await requireLiveAppSession(tx, session);
      const result = await tx.query<{ member: boolean }>(`SELECT EXISTS (
        SELECT 1 FROM space_members m JOIN spaces s ON s.id=m.space_id
        WHERE m.user_id=$1 AND m.space_id=$2 AND s.lifecycle_state='active'
      ) AS member`, [session.user_id, session.space_id]);
      return result.rows[0]?.member === true;
    }, { mode: "user", userId: session.user_id }),
    listRecords: (session) => withTransaction(pool, async (tx) => {
      await requireLiveAppSession(tx, session, "storage.read");
      const result = await tx.query<AppRecord>(`SELECT record_key AS key, data, created_at, updated_at
        FROM app_personal_records WHERE user_id=$1 AND app_id=$2 ORDER BY record_key`, [session.user_id, session.app_id]);
      return result.rows;
    }, { mode: "user", userId: session.user_id }),
    putRecord: (session, key, data) => withTransaction(pool, async (tx) => {
      await requireLiveAppSession(tx, session, "storage.write");
      const result = await tx.query<AppRecord>(`INSERT INTO app_personal_records (user_id, app_id, record_key, data)
        VALUES ($1, $2, $3, $4::jsonb)
        ON CONFLICT (user_id, app_id, record_key) DO UPDATE SET data=EXCLUDED.data, updated_at=NOW()
        RETURNING record_key AS key, data, created_at, updated_at`, [session.user_id, session.app_id, key, JSON.stringify(data)]);
      const record = result.rows[0];
      if (!record) throw new Error("Record write returned no row");
      return record;
    }, { mode: "user", userId: session.user_id }),
    deleteRecord: (session, key) => withTransaction(pool, async (tx) => {
      await requireLiveAppSession(tx, session, "storage.write");
      const result = await tx.query("DELETE FROM app_personal_records WHERE user_id=$1 AND app_id=$2 AND record_key=$3", [session.user_id, session.app_id, key]);
      return (result.rowCount ?? 0) > 0;
    }, { mode: "user", userId: session.user_id }),
  };
}
