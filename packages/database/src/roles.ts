import type { Pool } from "pg";

/** Refuse privileged runtime identities and accidental cross-service grants. */
export async function assertRuntimeDatabaseRole(pool: Pool, service: "api" | "payments"): Promise<void> {
  const result = await pool.query<{ privileged: boolean; billing_access: boolean; application_access: boolean; billing_create: boolean; operator_write: boolean }>(`
    SELECT (r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolcreatedb OR
      EXISTS(SELECT 1 FROM pg_auth_members m WHERE m.member=r.oid)) AS privileged,
      COALESCE((SELECT has_schema_privilege(r.oid,n.oid,'USAGE') FROM pg_namespace n WHERE n.nspname='billing'),false) AS billing_access,
      COALESCE((SELECT has_schema_privilege(r.oid,n.oid,'CREATE') FROM pg_namespace n WHERE n.nspname='billing'),false) AS billing_create,
      EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE ((n.nspname IN ('public','billing') AND c.relname IN ('goose_db_version','misty_migration_checksums'))
          OR (n.nspname='billing' AND c.relname IN ('cutover_checkpoints','legacy_subscription_records','legacy_checkout_records')))
        AND c.relkind IN ('r','p') AND has_table_privilege(r.oid,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')) AS operator_write,
      EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','f')
        AND has_table_privilege(r.oid,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')) AS application_access
    FROM pg_roles r WHERE r.rolname=current_user`);
  const role = result.rows[0];
  if (!role || role.privileged || role.operator_write || (service === "api" ? role.billing_access :
      !role.billing_access || role.billing_create || role.application_access)) {
    throw new Error("Runtime database role violates service isolation");
  }
}
