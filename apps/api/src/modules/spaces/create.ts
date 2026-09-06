import { createHash, randomUUID } from "node:crypto";
import type { PoolClient } from "pg";
import { readEntitlements } from "../entitlements/lookup.js";
import { SpaceError } from "./model.js";
import { initialRolePermissions } from "./permissions.js";
import { findTemplate, seedTemplate, spaceEvent } from "./templates.js";
// Existing Go retry records hash encoding/json's HTML-safe representation.
export function requestFingerprint(value: unknown) {
  const json = JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, (character) => `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`);
  return createHash("sha256").update(json).digest("hex");
}
/** Caller locks the active account and serializes this owner's creation paths. */
export async function createSpaceRows(tx: PoolClient, userId: string, name: string, templateId: string, providers: string[], onboarding = false) {
  await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`spaces:owner:${userId}`]);
  const count = BigInt((await tx.query<{ count: string }>("SELECT count(*) FROM spaces WHERE owner_user_id=$1 AND lifecycle_state<>'deleted'", [userId])).rows[0]!.count);
  if (count >= BigInt((await readEntitlements(tx, userId)).maxOwnedSpaces)) throw new SpaceError("space_ownership_limit_reached");
  const id = `space_${randomUUID()}`, domain = `sd_${randomUUID()}`;
  await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, userId, id]);
  await tx.query(`INSERT INTO spaces(id,owner_user_id,name,security_domain_id,is_default)
    SELECT $1,$2,$3,$4,NOT EXISTS(SELECT 1 FROM spaces WHERE owner_user_id=$2 AND is_default AND lifecycle_state='active')`, [id, userId, name, domain]);
  await tx.query("INSERT INTO space_storage_usage(space_id) VALUES($1)", [id]);
  await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner')", [id, userId]);
  await tx.query("INSERT INTO space_roles(id,space_id,name,is_everyone,permissions) VALUES($1,$2,'@everyone',true,$3::jsonb)", [`role_${randomUUID()}`, id, JSON.stringify(initialRolePermissions)]);
  for (const provider of providers) await tx.query("INSERT INTO space_setup_integrations(space_id,provider) VALUES($1,$2)", [id, provider]);
  await seedTemplate(tx, id, userId, findTemplate(templateId));
  await spaceEvent(tx, id, userId, "space.created", id, { name, template_id: templateId, integration_providers: providers, ...(onboarding ? { source: "onboarding" } : {}) });
  return id;
}
