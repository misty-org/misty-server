import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { type OfficialApp } from "../official-apps/catalog.js";
import { installAppInTransaction, installationColumns, installationResponse, type Installation } from "../official-apps/repository.js";
import { requireActiveAccount } from "../spaces/access.js";
import { createSpaceRows, requestFingerprint } from "../spaces/create.js";
import { normalizeSpaceName, SpaceError } from "../spaces/model.js";
import { readSpace } from "../spaces/read.js";
export function createOnboardingRepository(pool: Pool) {
  return {
    finish: (userId: string, rawName: string, apps: OfficialApp[]) => withTransaction(pool, async (tx) => {
      const name = normalizeSpaceName(rawName), fingerprint = requestFingerprint({ name, apps: [...apps].sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
        .map((app) => ({ ID: app.id, Version: app.version, PermissionVersion: app.permission_version, Scopes: app.scopes })) });
      await requireActiveAccount(tx, userId);
      await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`onboarding:${userId}`]);
      const previous = (await tx.query<{ request_fingerprint: string; space_id: string }>("SELECT request_fingerprint,space_id FROM onboarding_completions WHERE user_id=$1", [userId])).rows[0];
      let spaceId: string;
      if (previous) {
        if (previous.request_fingerprint !== fingerprint) throw new SpaceError("onboarding_request_changed");
        spaceId = previous.space_id;
      } else {
        // The owner lock also serializes ordinary Space creation with onboarding.
        await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`spaces:owner:${userId}`]);
        if ((await tx.query("SELECT id FROM spaces WHERE owner_user_id=$1 AND is_default AND lifecycle_state='active'", [userId])).rowCount) throw new SpaceError("onboarding_already_complete");
        spaceId = await createSpaceRows(tx, userId, name, "blank", [], true);
        for (const app of apps) await installAppInTransaction(tx, userId, app);
        await tx.query("INSERT INTO onboarding_completions(user_id,request_fingerprint,space_id) VALUES($1,$2,$3)", [userId, fingerprint, spaceId]);
      }
      const installed = (await tx.query<Installation>(`SELECT ${installationColumns} FROM user_app_installations WHERE user_id=$1 AND state<>'purged'
        ORDER BY CASE WHEN state='installed' THEN 0 ELSE 1 END,pinned DESC,pin_rank,app_id`, [userId])).rows.map(installationResponse);
      return { space: await readSpace(tx, userId, spaceId), apps: installed };
    }, { mode: "service" }),
  };
}
