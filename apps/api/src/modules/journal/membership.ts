import type { PoolClient } from "pg";

/** Caller holds the Space write lock. Preserve content while invalidating room tickets. */
export async function revokeSpaceCollaboration(tx: PoolClient, spaceId: string) {
  for (const kind of ["note", "drawing"] as const) {
    // Identifiers are module-owned constants. Keep the update and durable control
    // inserts in SQL so a large Space does not materialize every document in Node.
    await tx.query(`WITH changed AS (
      UPDATE space_${kind}s SET acl_version=acl_version+1,updated_at=now()
      WHERE space_id=$1 AND lifecycle_state IN ('active','archived','archived_creator_left') RETURNING id,acl_version
    ) INSERT INTO space_${kind}_control_outbox(id,${kind}_id,command,payload)
      SELECT '${kind}ctl_'||gen_random_uuid()::text,id,'acl',jsonb_build_object('acl_version',acl_version) FROM changed`, [spaceId]);
  }
}
