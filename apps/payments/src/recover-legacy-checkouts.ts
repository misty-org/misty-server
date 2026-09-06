import { createDatabasePool } from "../../../packages/database/src/pool.js";
import { legacyCheckoutRecoveryReport, retryLegacyCheckoutReview } from "./modules/legacy-checkout/operations.js";

const args = process.argv.slice(2);
if (args.length && (args.length !== 2 || !["--retry", "--after"].includes(args[0] ?? "") || !args[1])) throw new Error("Use no arguments or --after <attempt-id> for a report, or --retry <attempt-id> after investigating the review reason");
if (!process.env.BILLING_DB_MIGRATION_USER || !process.env.BILLING_DB_MIGRATION_PASSWORD) throw new Error("Separate BILLING_DB_MIGRATION_USER and BILLING_DB_MIGRATION_PASSWORD are required");
const pool = createDatabasePool({ ...process.env, BILLING_DB_USER: process.env.BILLING_DB_MIGRATION_USER, BILLING_DB_PASSWORD: process.env.BILLING_DB_MIGRATION_PASSWORD }, "payments");
try { console.log(JSON.stringify(args[0] === "--retry" ? await retryLegacyCheckoutReview(pool, args[1]!) : await legacyCheckoutRecoveryReport(pool, args[1] ?? null))); }
catch { console.error("Legacy checkout recovery operation failed; inspect the migration-role access and requested attempt."); process.exitCode = 1; }
finally { await pool.end(); }
