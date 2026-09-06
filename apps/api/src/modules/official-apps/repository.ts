import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { OfficialApp } from "./catalog.js";

export class InstallationError extends Error {
  constructor(readonly code: "app_not_found" | "app_not_installed" | "app_data_purging" | "app_runtime_forbidden" | "account_unavailable") { super(code); }
}
export const installationColumns = "app_id,state,installed_version,permission_version,granted_scopes,pinned,pin_rank,installed_at,uninstalled_at,data_deletion_at,purged_at,updated_at";
export type Installation = { app_id: string; state: string; installed_version: string; permission_version: number; granted_scopes: string[]; pinned: boolean;
  pin_rank: string; installed_at: Date; uninstalled_at: Date | null; data_deletion_at: Date | null; purged_at: Date | null; updated_at: Date };
export function installationResponse(row: Installation) {
  const { uninstalled_at, data_deletion_at, purged_at, pin_rank, ...rest } = row;
  const rank = Number(pin_rank);
  if (!Number.isSafeInteger(rank)) throw new Error("App pin rank exceeds supported client precision");
  return { ...rest, pin_rank: rank, ...(uninstalled_at ? { uninstalled_at } : {}), ...(data_deletion_at ? { data_deletion_at } : {}), ...(purged_at ? { purged_at } : {}) };
}
async function lockAccount(tx: PoolClient, userId: string) {
  if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [userId])).rowCount) throw new InstallationError("account_unavailable");
}
async function lockInstallation(tx: PoolClient, userId: string, appId: string) {
  await lockAccount(tx, userId);
  // Serialize missing-row creation as well as install/uninstall/pin changes.
  await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`apps:install:${userId}:${appId}`]);
  return (await tx.query<Installation>(`SELECT ${installationColumns} FROM user_app_installations WHERE user_id=$1 AND app_id=$2 FOR UPDATE`, [userId, appId])).rows[0];
}
async function event(tx: PoolClient, userId: string, appId: string, type: string, metadata: object = {}) {
  await tx.query("INSERT INTO app_install_events(user_id,app_id,event_type,metadata) VALUES($1,$2,$3,$4::jsonb)", [userId, appId, type, JSON.stringify(metadata)]);
}
export async function installAppInTransaction(tx: PoolClient, userId: string, app: OfficialApp) {
  const previous = await lockInstallation(tx, userId, app.id);
  if (previous?.state === "purging") throw new InstallationError("app_data_purging");
  const row = (await tx.query<Installation>(`INSERT INTO user_app_installations
    (user_id,app_id,state,installed_version,permission_version,granted_scopes,pinned,pin_rank)
    VALUES($1,$2,'installed',$3,$4,$5::jsonb,TRUE,COALESCE((SELECT MAX(pin_rank)+1024 FROM user_app_installations WHERE user_id=$1 AND state='installed'),1024))
    ON CONFLICT(user_id,app_id) DO UPDATE SET state='installed',installed_version=EXCLUDED.installed_version,
    permission_version=EXCLUDED.permission_version,granted_scopes=EXCLUDED.granted_scopes,
    pinned=CASE WHEN user_app_installations.state='installed' THEN user_app_installations.pinned ELSE TRUE END,
    pin_rank=CASE WHEN user_app_installations.state='installed' THEN user_app_installations.pin_rank ELSE EXCLUDED.pin_rank END,
    installed_at=CASE WHEN user_app_installations.state='installed' THEN user_app_installations.installed_at ELSE now() END,
    uninstalled_at=NULL,data_deletion_at=NULL,purged_at=NULL,updated_at=now() RETURNING ${installationColumns}`,
  [userId, app.id, app.version, app.permission_version, JSON.stringify(app.scopes)])).rows[0]!;
  await tx.query("DELETE FROM app_runtime_sessions WHERE user_id=$1 AND app_id=$2 AND scopes<>$3::jsonb", [userId, app.id, JSON.stringify(app.scopes)]);
  await tx.query("DELETE FROM app_data_deletion_jobs WHERE user_id=$1 AND app_id=$2", [userId, app.id]);
  const type = previous?.state === "recoverable" ? "restored" : previous?.state === "installed" && previous.installed_version !== app.version ? "updated" : "installed";
  await event(tx, userId, app.id, type, { version: app.version, permission_version: app.permission_version });
  return installationResponse(row);
}
export function createInstallationRepository(pool: Pool, clock = () => new Date()) {
  return {
    list: (userId: string) => withTransaction(pool, async (tx) => {
      await lockAccount(tx, userId);
      return (await tx.query<Installation>(`SELECT ${installationColumns} FROM user_app_installations WHERE user_id=$1 AND state<>'purged'
        ORDER BY CASE WHEN state='installed' THEN 0 ELSE 1 END,pinned DESC,pin_rank,app_id`, [userId])).rows.map(installationResponse);
    }, { mode: "user", userId }),
    install: (userId: string, app: OfficialApp) => withTransaction(pool, async (tx) => {
      return installAppInTransaction(tx, userId, app);
    }, { mode: "user", userId }),
    pin: (userId: string, appId: string, pinned: boolean) => withTransaction(pool, async (tx) => {
      const previous = await lockInstallation(tx, userId, appId);
      if (previous?.state !== "installed") throw new InstallationError("app_not_installed");
      const row = (await tx.query<Installation>(`UPDATE user_app_installations SET pinned=$3,
        pin_rank=CASE WHEN $3 AND NOT pinned THEN COALESCE((SELECT MAX(i.pin_rank)+1024 FROM user_app_installations i
          WHERE i.user_id=$1 AND i.state='installed' AND i.app_id<>$2),1024) ELSE pin_rank END,updated_at=now()
        WHERE user_id=$1 AND app_id=$2 RETURNING ${installationColumns}`, [userId, appId, pinned])).rows[0]!;
      await event(tx, userId, appId, pinned ? "pinned" : "unpinned");
      return installationResponse(row);
    }, { mode: "user", userId }),
    uninstall: (userId: string, appId: string) => withTransaction(pool, async (tx) => {
      const previous = await lockInstallation(tx, userId, appId);
      if (!previous || previous.state === "purged") throw new InstallationError("app_not_found");
      if (previous.state === "purging") throw new InstallationError("app_data_purging");
      if (previous.state === "recoverable") return installationResponse(previous);
      const now = clock(), deleteAt = new Date(now.getTime() + 30 * 86400_000);
      const row = (await tx.query<Installation>(`UPDATE user_app_installations SET state='recoverable',pinned=FALSE,
        uninstalled_at=$3,data_deletion_at=$4,purged_at=NULL,updated_at=$3 WHERE user_id=$1 AND app_id=$2 RETURNING ${installationColumns}`, [userId, appId, now, deleteAt])).rows[0]!;
      // An old token must not revive when a recoverable installation is restored.
      await tx.query("DELETE FROM app_runtime_sessions WHERE user_id=$1 AND app_id=$2", [userId, appId]);
      await tx.query(`INSERT INTO app_data_deletion_jobs(user_id,app_id,delete_at) VALUES($1,$2,$3)
        ON CONFLICT(user_id,app_id) DO UPDATE SET delete_at=EXCLUDED.delete_at,state='pending',attempts=0,last_error='',
        started_at=NULL,completed_at=NULL,updated_at=now()`, [userId, appId, deleteAt]);
      await event(tx, userId, appId, "uninstalled", { data_deletion_at: deleteAt });
      return installationResponse(row);
    }, { mode: "user", userId }),
    session: (userId: string, appId: string, tokenHash: string, spaceId: string) => withTransaction(pool, async (tx) => {
      await lockAccount(tx, userId);
      if (spaceId && !(await tx.query(`SELECT m.user_id FROM space_members m JOIN spaces s ON s.id=m.space_id
        WHERE m.user_id=$1 AND m.space_id=$2 AND s.lifecycle_state='active'`, [userId, spaceId])).rowCount) throw new InstallationError("app_runtime_forbidden");
      const installed = (await tx.query<{ granted_scopes: string[] }>(`SELECT granted_scopes FROM user_app_installations
        WHERE user_id=$1 AND app_id=$2 AND state='installed' FOR SHARE`, [userId, appId])).rows[0];
      if (!installed) throw new InstallationError("app_not_installed");
      const expiresAt = new Date(clock().getTime() + 300_000);
      await tx.query(`INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,space_id,scopes,expires_at)
        VALUES($1,$2,$3,NULLIF($4,''),$5::jsonb,$6)`, [tokenHash, userId, appId, spaceId, JSON.stringify(installed.granted_scopes), expiresAt]);
      return { app_id: appId, space_id: spaceId, scopes: installed.granted_scopes, expires_at: expiresAt };
    }, { mode: "user", userId }),
  };
}
