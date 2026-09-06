import { afterAll, describe, expect, it } from "vitest";
import { fileURLToPath } from "node:url";
import { applyMigrations, readMigrations } from "./migrations.js";
import { createTestDatabase } from "./test-database.js";

const pool = createTestDatabase();
afterAll(() => pool.end());

describe("existing Go migration compatibility", () => {
  it("applies the full SQL history without Go and is safe to rerun", async () => {
    const directory = fileURLToPath(new URL("../../../internal/platform/postgres/migrations/", import.meta.url));
    const migrations = await readMigrations(directory);
    expect(migrations.length).toBeGreaterThanOrEqual(143);
    await applyMigrations(pool, migrations);
    expect(await applyMigrations(pool, migrations)).toEqual([]);
    const latest = await pool.query("SELECT MAX(version_id) AS version FROM goose_db_version WHERE is_applied = true");
    expect(latest.rows[0].version).toBe(migrations.at(-1)?.version);
  }, 60000);
});
