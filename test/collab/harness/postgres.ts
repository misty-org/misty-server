import { randomBytes } from "node:crypto";
import { existsSync } from "node:fs";
import path from "node:path";
import { baseEnv, run, serverRoot, start } from "./processes.js";

const IMAGE = "pgvector/pgvector:pg16@sha256:1d533553fefe4f12e5d80c7b80622ba0c382abb5758856f52983d8789179f0fb";
const GOOSE_TAGS = "no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb";

export interface DatabaseAccess {
  host: string;
  port: number;
  name: string;
  user: string;
  password: string;
}

export interface Postgres {
  container: string;
  owner: DatabaseAccess;
  app: DatabaseAccess;
  stop(): Promise<void>;
}

export const dsnOf = (access: DatabaseAccess) =>
  `host=${access.host} port=${access.port} user=${access.user} password=${access.password} dbname=${access.name} sslmode=disable`;

export async function startPostgres(): Promise<Postgres> {
  const container = `misty-collab-runtime-${process.pid}-${randomBytes(3).toString("hex")}`;
  const password = `owner-${randomBytes(12).toString("hex")}`;
  const appPassword = `app-${randomBytes(12).toString("hex")}`;
  await run("docker", [
    "run", "-d", "--rm", "--name", container, "-p", "127.0.0.1::5432",
    "--tmpfs", "/var/lib/postgresql/data:rw,size=2g",
    "-e", "POSTGRES_USER=misty", "-e", `POSTGRES_PASSWORD=${password}`, "-e", "POSTGRES_DB=misty_collab_test",
    IMAGE, "-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off", "-c", "max_connections=400",
  ]);
  const stop = async () => {
    await run("docker", ["rm", "-fv", container]).catch(() => undefined);
  };
  try {
    const logs = start("docker", ["logs", "-f", container], baseEnv());
    try {
      await logs.waitFor(/database system is ready to accept connections/, 120_000, 2);
    } finally {
      await logs.stop("SIGTERM", 5_000);
    }
    const mapping = (await run("docker", ["port", container, "5432/tcp"])).stdout.trim().split("\n")[0] ?? "";
    const port = Number(mapping.slice(mapping.lastIndexOf(":") + 1));
    if (!Number.isInteger(port) || port <= 0) throw new Error(`docker port said ${mapping}`);
    const owner: DatabaseAccess = { host: "127.0.0.1", port, name: "misty_collab_test", user: "misty", password };
    await migrate(owner, appPassword);
    return { container, owner, app: { ...owner, user: "misty_app", password: appPassword }, stop };
  } catch (error) {
    await stop();
    throw error;
  }
}

async function migrate(owner: DatabaseAccess, appPassword: string) {
  const bin = path.join(serverRoot, ".misty/bin");
  const goose = path.join(bin, "goose");
  if (!existsSync(goose)) {
    await run("go", ["install", `-tags=${GOOSE_TAGS}`, "github.com/pressly/goose/v3/cmd/goose@v3.27.3"], {
      env: baseEnv({ GOBIN: bin }),
    });
  }
  await run(goose, ["-dir", "internal/platform/db/migrations", "postgres", dsnOf(owner), "up"]);
  await run("go", ["run", "./cmd/misty-dev-setup", "grant-app-role"], {
    env: baseEnv({
      PGHOST: owner.host,
      PGPORT: String(owner.port),
      PGUSER: owner.user,
      PGPASSWORD: owner.password,
      PGDATABASE: owner.name,
      MISTY_APP_DB_USER: "misty_app",
      MISTY_APP_DB_PASSWORD: appPassword,
    }),
  });
}

export function databaseEnv(access: DatabaseAccess): Record<string, string> {
  return {
    DB_HOST: access.host,
    DB_PORT: String(access.port),
    DB_USER: access.user,
    DB_PASSWORD: access.password,
    DB_NAME: access.name,
  };
}
