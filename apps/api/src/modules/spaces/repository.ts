import type { Pool } from "pg";
import { removeMembership } from "./membership.js";
import { transferOwnership } from "./ownership.js";
import { requestSpaceDeletion } from "./deletion.js";
import { readSpaceMembers, memberPermissions, type PermissionChange } from "./members.js";
import { readSpaceAgents } from "../agents/space-memberships.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { requireActiveAccount, requireSpaceActor, type SpaceActor } from "./access.js";
import { readSpace, readSpaces, readSetup } from "./read.js";
import { createSpaceRows, requestFingerprint } from "./create.js";
import { normalizeSpaceName, SpaceError, trimSpace } from "./model.js";
import { findTemplate, normalizeProviders, spaceEvent } from "./templates.js";
import { entitlementResponse, readEntitlements } from "../entitlements/lookup.js";
import { personalStorageSummary } from "../storage/summary.js";
export function createSpaceRepository(pool: Pool) {
  return {
    delete: (actor: SpaceActor, spaceId: string, confirmation: string) => withTransaction(pool,
      (tx) => requestSpaceDeletion(tx, actor, spaceId, confirmation), { mode: "service" }),
    transfer: (actor: SpaceActor, spaceId: string, targetId: string) => withTransaction(pool,
      (tx) => transferOwnership(tx, actor, spaceId, targetId), { mode: "service" }),
    removeMember: (actor: SpaceActor, spaceId: string, memberId: string) => withTransaction(pool,
      (tx) => removeMembership(tx, actor, spaceId, memberId), { mode: "service" }),
    leave: (actor: SpaceActor, spaceId: string) => withTransaction(pool,
      (tx) => removeMembership(tx, actor, spaceId), { mode: "service" }),
    members: (actor: SpaceActor, spaceId: string) => withTransaction(pool, (tx) => readSpaceMembers(tx, actor, spaceId), { mode: "service" }),
    agents: (actor: SpaceActor, spaceId: string) => withTransaction(pool, async (tx) => {
      await requireSpaceActor(tx, actor, spaceId); return { agents: await readSpaceAgents(tx, actor.userId, spaceId) };
    }, { mode: "service" }),
    permissions: (actor: SpaceActor, spaceId: string, memberId: string, change?: PermissionChange) => withTransaction(pool,
      (tx) => memberPermissions(tx, actor, spaceId, memberId, change), { mode: "service" }),
    list: (userId: string) => withTransaction(pool, async (tx) => {
      await requireActiveAccount(tx, userId);
      const spaces = await readSpaces(tx, userId);
      const invitations = (await tx.query(`SELECT i.id,i.space_id,s.name AS space_name,i.invited_user_id,invited.name AS invited_user_name,
        i.invited_email,i.invited_by_user_id,inviter.name AS inviter_name,i.delivery_status,i.expires_at,i.created_at FROM space_invitations i
        JOIN spaces s ON s.id=i.space_id JOIN users current_invitee ON current_invitee.id=$1 JOIN users inviter ON inviter.id=i.invited_by_user_id
        LEFT JOIN users invited ON invited.id=i.invited_user_id WHERE (i.invited_user_id=$1 OR lower(i.invited_email)=lower(current_invitee.email))
        AND i.revoked_at IS NULL AND i.consumed_at IS NULL AND i.expires_at>now() AND s.lifecycle_state='active' ORDER BY i.created_at DESC,i.id`, [userId])).rows;
      return { spaces, invitations, entitlements: entitlementResponse(await readEntitlements(tx, userId)), owner_storage: await personalStorageSummary(tx, userId) };
    }, { mode: "service" }),
    get: (actor: SpaceActor, spaceId: string) => withTransaction(pool, async (tx) => {
      try { await requireSpaceActor(tx, actor, spaceId); }
      catch (error) { if (!actor.appSession && error instanceof SpaceError && error.code === "forbidden") throw new SpaceError("not_found"); throw error; }
      return readSpace(tx, actor.userId, spaceId);
    }, { mode: "service" }),
    create: (userId: string, input: { name: string; template_id: string; integration_providers: string[] }, key: string) => withTransaction(pool, async (tx) => {
      const name = normalizeSpaceName(input.name), template = findTemplate(input.template_id), providers = normalizeProviders(input.integration_providers), idempotencyKey = trimSpace(key);
      if (Buffer.byteLength(idempotencyKey) > 200) throw new SpaceError("invalid_request");
      const fingerprint = requestFingerprint({ name, template: template.id, providers });
      await requireActiveAccount(tx, userId);
      let spaceId: string | undefined;
      if (idempotencyKey) {
        await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`spaces:create:${userId}:${idempotencyKey}`]);
        const previous = (await tx.query<{ request_fingerprint: string; space_id: string }>("SELECT request_fingerprint,space_id FROM space_creation_requests WHERE user_id=$1 AND idempotency_key=$2", [userId, idempotencyKey])).rows[0];
        if (previous) {
          if (previous.request_fingerprint !== fingerprint) throw new SpaceError("version_conflict");
          spaceId = previous.space_id;
        }
      }
      if (!spaceId) {
        spaceId = await createSpaceRows(tx, userId, name, template.id, providers);
        if (idempotencyKey) await tx.query("INSERT INTO space_creation_requests(user_id,idempotency_key,request_fingerprint,space_id) VALUES($1,$2,$3,$4)", [userId, idempotencyKey, fingerprint, spaceId]);
      }
      return { space: await readSpace(tx, userId, spaceId), setup: await readSetup(tx, spaceId) };
    }, { mode: "service" }),
    rename: (actor: SpaceActor, spaceId: string, rawName: string) => withTransaction(pool, async (tx) => {
      const name = normalizeSpaceName(rawName); await requireSpaceActor(tx, actor, spaceId, true);
      await tx.query("UPDATE spaces SET name=$2,updated_at=now() WHERE id=$1", [spaceId, name]);
      await spaceEvent(tx, spaceId, actor.userId, "space.updated", spaceId, { name }); return readSpace(tx, actor.userId, spaceId);
    }, { mode: "service" }),
    setup: (actor: SpaceActor, spaceId: string, update?: { provider: string; status: string }) => withTransaction(pool, async (tx) => {
      await requireSpaceActor(tx, actor, spaceId, !!update);
      if (update) {
        normalizeProviders([update.provider]);
        if (!["selected", "authorized", "configured", "skipped"].includes(update.status)) throw new SpaceError("invalid_request");
        if (!(await tx.query("UPDATE space_setup_integrations SET status=$3,updated_at=now() WHERE space_id=$1 AND provider=$2", [spaceId, update.provider, update.status])).rowCount) throw new SpaceError("not_found");
      }
      return readSetup(tx, spaceId);
    }, { mode: "service" }),
  };
}
