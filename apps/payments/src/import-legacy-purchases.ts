import { createDatabasePool } from "../../../packages/database/src/pool.js";
import { importLegacyPurchases } from "./modules/legacy-purchases/import.js";

const args = process.argv.slice(2);
if (args.some((arg) => arg !== "--commit") || args.length > 1) throw new Error("Use no arguments for dry run, or --commit after Go billing writers are stopped");
if (!process.env.BILLING_DB_MIGRATION_USER || !process.env.BILLING_DB_MIGRATION_PASSWORD) {
  throw new Error("Separate BILLING_DB_MIGRATION_USER and BILLING_DB_MIGRATION_PASSWORD are required");
}
const pool = createDatabasePool({ ...process.env,
  BILLING_DB_USER: process.env.BILLING_DB_MIGRATION_USER,
  BILLING_DB_PASSWORD: process.env.BILLING_DB_MIGRATION_PASSWORD,
}, "payments");
try {
  console.log(JSON.stringify(await importLegacyPurchases(pool, { commit: args.includes("--commit") })));
} catch {
  // Database exceptions can contain customer identifiers; keep the CLI output private-data free.
  console.error("Legacy purchase import failed. No partial import was committed; review source/target consistency and migration-role access.");
  process.exitCode = 1;
} finally {
  await pool.end();
}
