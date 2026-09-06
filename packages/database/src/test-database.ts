import { Pool } from "pg";

/** Never infer test credentials from normal application configuration. */
export function createTestDatabase(): Pool {
  const connectionString = process.env.MISTY_TEST_DATABASE_URL;
  if (!connectionString) throw new Error("Set MISTY_TEST_DATABASE_URL to an isolated PostgreSQL test database");
  const url = new URL(connectionString);
  if (!url.pathname.endsWith("_test")) throw new Error("Integration database name must end in _test");
  return new Pool({ connectionString, max: 2, connectionTimeoutMillis: 5000 });
}
