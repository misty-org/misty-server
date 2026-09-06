import { resolve } from "node:path";
import { createDatabasePool } from "../../../packages/database/src/pool.js";
import { applyMigrations, readMigrations } from "../../../packages/database/src/migrations.js";

if (!process.env.DB_MIGRATION_USER || !process.env.DB_MIGRATION_PASSWORD) {
  throw new Error("DB_MIGRATION_USER and DB_MIGRATION_PASSWORD are required; application credentials are not migration credentials");
}
const pool = createDatabasePool({
  ...process.env,
  DB_USER: process.env.DB_MIGRATION_USER,
  DB_PASSWORD: process.env.DB_MIGRATION_PASSWORD,
}, "api");
try {
  const directory = resolve(process.env.MIGRATIONS_DIR ?? "internal/platform/postgres/migrations");
  const applied = await applyMigrations(pool, await readMigrations(directory));
  console.log(JSON.stringify({ applied }));
} finally {
  await pool.end();
}
