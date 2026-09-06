import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";

export type AccountSettings = { email_updates_enabled: boolean; analytics_enabled: boolean; error_reporting_enabled: boolean };
export class AccountUnavailable extends Error {}
export function createAccountRepository(pool: Pool) {
  const update = (userId: string, sql: string, values: unknown[]) => withTransaction(pool, async (tx) => {
    if (!(await tx.query(sql, [userId, ...values])).rowCount) throw new AccountUnavailable();
  }, { mode: "user", userId });
  return {
    settings: (userId: string) => withTransaction(pool, async (tx) => {
      const row = (await tx.query<AccountSettings>(`SELECT email_updates_enabled,analytics_enabled,error_reporting_enabled
        FROM users WHERE id=$1 AND lifecycle_state='active'`, [userId])).rows[0];
      if (!row) throw new AccountUnavailable();
      return row;
    }, { mode: "user", userId }),
    name: (userId: string, name: string) => update(userId, "UPDATE users SET name=$2 WHERE id=$1 AND lifecycle_state='active'", [name]),
    settingsUpdate: (userId: string, settings: AccountSettings) => update(userId,
      `UPDATE users SET email_updates_enabled=$2,analytics_enabled=$3,error_reporting_enabled=$4 WHERE id=$1 AND lifecycle_state='active'`,
      [settings.email_updates_enabled, settings.analytics_enabled, settings.error_reporting_enabled]),
    telemetry: (userId: string, analytics: boolean, errors: boolean) => update(userId,
      "UPDATE users SET analytics_enabled=$2,error_reporting_enabled=$3 WHERE id=$1 AND lifecycle_state='active'", [analytics, errors]),
    device: (userId: string, device: string) => withTransaction(pool, async (tx) => {
      // Account first, then license: serialize with deletion and entitlement changes.
      if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [userId])).rowCount) throw new AccountUnavailable();
      if (!(await tx.query("UPDATE licenses SET license_device=$2,updated_at=now() WHERE user_id=$1", [userId, device])).rowCount) throw new AccountUnavailable();
    }, { mode: "user", userId }),
  };
}
