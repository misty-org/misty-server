import { createHash } from "node:crypto";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import type { Pool } from "pg";

export interface SqlMigration {
  version: string;
  name: string;
  up: string;
  checksum: string;
}

export function parseMigration(name: string, source: string): SqlMigration {
  const match = /^(\d+)_[a-zA-Z0-9_]+\.sql$/.exec(name);
  if (!match?.[1]) throw new Error(`Invalid migration filename: ${name}`);
  if (/^\s*--\s*\+goose\s+(NO TRANSACTION|ENVSUB)/m.test(source)) {
    throw new Error(`Migration ${name} requires unsupported execution directives`);
  }
  const upMarker = /^\s*--\s*\+goose Up\s*$/m.exec(source);
  if (!upMarker) throw new Error(`Migration ${name} has no Up section`);
  const tail = source.slice(upMarker.index + upMarker[0].length);
  const downMarker = /^\s*--\s*\+goose Down\s*$/m.exec(tail);
  const up = (downMarker ? tail.slice(0, downMarker.index) : tail).trim();
  if (!up) throw new Error(`Migration ${name} has an empty Up section`);
  // Preserve entire SQL batches, including PL/pgSQL function bodies and dollar quotes.
  return { version: BigInt(match[1]).toString(), name, up, checksum: createHash("sha256").update(source).digest("hex") };
}

export async function readMigrations(directory: string): Promise<SqlMigration[]> {
  const names = (await readdir(directory)).filter((name) => name.endsWith(".sql")).sort();
  const migrations = await Promise.all(names.map(async (name) => parseMigration(name, await readFile(join(directory, name), "utf8"))));
  const versions = migrations.map((migration) => migration.version);
  if (new Set(versions).size !== versions.length) throw new Error("Duplicate migration versions");
  return migrations.sort((a, b) => BigInt(a.version) < BigInt(b.version) ? -1 : 1);
}

/** Forward-only runner; operators restore/reconcile using the release rollback plan. */
export async function applyMigrations(
  pool: Pool,
  migrations: readonly SqlMigration[],
  namespace: "public" | "billing" = "public",
): Promise<string[]> {
  // Identifiers are restricted to owned schemas, never supplied by a request.
  if (namespace !== "public" && namespace !== "billing") throw new Error("Invalid migration namespace");
  const lockId = namespace === "public" ? 824717315 : 824717316;
  const client = await pool.connect();
  const applied: string[] = [];
  let discard = false;
  let locked = false;
  try {
    await client.query("SELECT pg_advisory_lock($1)", [lockId]);
    locked = true;
    if (namespace === "billing") {
      await client.query("CREATE SCHEMA IF NOT EXISTS billing; REVOKE ALL ON SCHEMA billing FROM PUBLIC");
    }
    await client.query(`CREATE TABLE IF NOT EXISTS ${namespace}.goose_db_version (
      id BIGSERIAL PRIMARY KEY, version_id BIGINT NOT NULL, is_applied BOOLEAN NOT NULL,
      tstamp TIMESTAMP NOT NULL DEFAULT now()
    )`);
    await client.query(`CREATE TABLE IF NOT EXISTS ${namespace}.misty_migration_checksums (
      version_id BIGINT PRIMARY KEY, checksum TEXT NOT NULL, name TEXT NOT NULL,
      recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )`);
    const history = await client.query<{ version_id: string; is_applied: boolean }>(
      `SELECT DISTINCT ON (version_id) version_id, is_applied FROM ${namespace}.goose_db_version ORDER BY version_id, id DESC`,
    );
    const existing = new Set(history.rows.filter((row) => row.is_applied).map((row) => row.version_id));
    const checksumResult = await client.query<{ version_id: string; checksum: string }>(`SELECT version_id, checksum FROM ${namespace}.misty_migration_checksums`);
    const checksums = new Map(checksumResult.rows.map((row) => [row.version_id, row.checksum]));
    for (const migration of migrations) {
      const knownChecksum = checksums.get(migration.version);
      if (knownChecksum !== undefined && knownChecksum !== migration.checksum) {
        throw new Error(`Applied migration changed: ${migration.name}`);
      }
      if (existing.has(migration.version)) continue;
      await client.query("BEGIN");
      try {
        await client.query(migration.up);
        await client.query(`INSERT INTO ${namespace}.goose_db_version (version_id, is_applied) VALUES ($1, true)`, [migration.version]);
        await client.query(`INSERT INTO ${namespace}.misty_migration_checksums (version_id, checksum, name) VALUES ($1, $2, $3) ON CONFLICT (version_id) DO NOTHING`, [migration.version, migration.checksum, migration.name]);
        await client.query("COMMIT");
        applied.push(migration.name);
      } catch (error) {
        try { await client.query("ROLLBACK"); } catch { discard = true; }
        throw new Error(`Migration failed: ${migration.name}`, { cause: error });
      }
    }
    return applied;
  } finally {
    if (locked && !discard) {
      try { await client.query("SELECT pg_advisory_unlock($1)", [lockId]); } catch { discard = true; }
    }
    client.release(discard);
  }
}
