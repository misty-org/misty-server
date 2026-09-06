import { Pool } from "pg";
import { z } from "zod";
import type { Environment } from "../../runtime/src/config.js";

const schema = z.object({
  host: z.string().min(1),
  port: z.coerce.number().int().min(1).max(65535),
  database: z.string().min(1),
  user: z.string().min(1),
  password: z.string().min(1),
  max: z.coerce.number().int().min(1).max(100),
  sslMode: z.enum(["disable", "verify-full"]),
});

export function createDatabasePool(env: Environment, service: "api" | "payments"): Pool {
  const prefix = service === "payments" ? "BILLING_DB_" : "DB_";
  const config = schema.parse({
    host: env[`${prefix}HOST`],
    port: env[`${prefix}PORT`] ?? "5432",
    database: env[`${prefix}NAME`],
    user: env[`${prefix}USER`],
    password: env[`${prefix}PASSWORD`],
    max: env[`${prefix}POOL_SIZE`] ?? "10",
    sslMode: env[`${prefix}SSLMODE`] ?? "verify-full",
  });
  return new Pool({
    host: config.host,
    port: config.port,
    database: config.database,
    user: config.user,
    password: config.password,
    max: config.max,
    application_name: `misty-${service}`,
    ssl: config.sslMode === "disable" ? false : { rejectUnauthorized: true },
    connectionTimeoutMillis: 5000,
    idleTimeoutMillis: 30000,
    statement_timeout: 30000,
    query_timeout: 35000,
    idle_in_transaction_session_timeout: 30000,
  });
}
