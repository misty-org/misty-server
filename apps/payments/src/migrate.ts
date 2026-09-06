import { resolve } from "node:path";
import { createDatabasePool } from "../../../packages/database/src/pool.js";
import { applyMigrations, readMigrations } from "../../../packages/database/src/migrations.js";

if (!process.env.BILLING_DB_MIGRATION_USER || !process.env.BILLING_DB_MIGRATION_PASSWORD) {
  throw new Error("Separate BILLING_DB_MIGRATION_USER and BILLING_DB_MIGRATION_PASSWORD are required");
}
const pool = createDatabasePool({
  ...process.env,
  BILLING_DB_USER: process.env.BILLING_DB_MIGRATION_USER,
  BILLING_DB_PASSWORD: process.env.BILLING_DB_MIGRATION_PASSWORD,
}, "payments");
try {
  const directory = resolve(process.env.BILLING_MIGRATIONS_DIR ?? "apps/payments/migrations");
  const applied = await applyMigrations(pool, await readMigrations(directory), "billing");
  console.log(JSON.stringify({ applied }));
} finally {
  await pool.end();
}
