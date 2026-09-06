import { randomUUID } from "node:crypto";
import { afterAll, expect, it } from "vitest";
import { createTestDatabase } from "./test-database.js";
import { applyMigrations, parseMigration } from "./migrations.js";

const pool = createTestDatabase();
const suffix = randomUUID().replaceAll("-", "");
const table = `migration_test_${suffix}`;
const successVersion = "90000000000001";
const failureVersion = "90000000000002";
afterAll(async () => {
  await pool.query(`DROP TABLE IF EXISTS billing.${table}`);
  await pool.query("DELETE FROM billing.goose_db_version WHERE version_id=ANY($1::bigint[])", [[successVersion, failureVersion]]);
  await pool.query("DELETE FROM billing.misty_migration_checksums WHERE version_id=ANY($1::bigint[])", [[successVersion, failureVersion]]);
  await pool.end();
});

it("serializes concurrent migration runners and rejects changed applied SQL", async () => {
  const migration = parseMigration(`${successVersion}_atomic_test.sql`, `-- +goose Up\nCREATE TABLE billing.${table}(id INTEGER);`);
  const results = await Promise.all([applyMigrations(pool, [migration], "billing"), applyMigrations(pool, [migration], "billing")]);
  expect(results.flat()).toEqual([migration.name]);
  await expect(applyMigrations(pool, [{ ...migration, checksum: "changed" }], "billing")).rejects.toThrow("Applied migration changed");
});

it("rolls back all statements and the version ledger when a migration fails", async () => {
  const migration = parseMigration(`${failureVersion}_rollback_test.sql`, `-- +goose Up
    INSERT INTO billing.${table}(id) VALUES(1);
    SELECT deliberately_missing_migration_column;`);
  await expect(applyMigrations(pool, [migration], "billing")).rejects.toThrow("Migration failed");
  expect((await pool.query(`SELECT count(*) FROM billing.${table}`)).rows[0].count).toBe("0");
  expect((await pool.query("SELECT 1 FROM billing.goose_db_version WHERE version_id=$1", [failureVersion])).rowCount).toBe(0);
});
