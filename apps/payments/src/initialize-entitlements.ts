import { createDatabasePool } from "../../../packages/database/src/pool.js";
import { initializeSubscriptionEntitlements } from "./modules/entitlements/initialize.js";

const args = process.argv.slice(2);
if (args.some((arg) => arg !== "--commit") || args.length > 1) throw new Error("Use no arguments for dry run, or --commit after all Go/native billing and license writers are stopped");
if (!process.env.BILLING_DB_MIGRATION_USER || !process.env.BILLING_DB_MIGRATION_PASSWORD) throw new Error("Separate BILLING_DB_MIGRATION_USER and BILLING_DB_MIGRATION_PASSWORD are required");
const pool = createDatabasePool({ ...process.env, BILLING_DB_USER: process.env.BILLING_DB_MIGRATION_USER, BILLING_DB_PASSWORD: process.env.BILLING_DB_MIGRATION_PASSWORD }, "payments");
try { console.log(JSON.stringify(await initializeSubscriptionEntitlements(pool, { commit: args.includes("--commit") }))); }
catch {
  console.error("Initial entitlement handover failed. No partial batch was committed; review verified imports, license state and migration-role access.");
  process.exitCode = 1;
} finally { await pool.end(); }
