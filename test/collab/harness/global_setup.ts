import { randomBytes } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import type { TestProject } from "vitest/node";
import { databaseEnv, startPostgres, type DatabaseAccess } from "./postgres.js";
import { baseEnv, run, start } from "./processes.js";

export interface Person {
  id: string;
  username: string;
}

export interface Setup {
  owner: DatabaseAccess;
  app: DatabaseAccess;
  collab: string;
  signingKey: string;
  fixture: string;
  people: Record<"ada" | "ben" | "cyd" | "dee" | "eve", Person>;
}

declare module "vitest" {
  export interface ProvidedContext {
    setup: Setup;
  }
}

export default async function globalSetup(project: TestProject) {
  const folder = await mkdtemp(path.join(os.tmpdir(), "misty-collab-runtime-"));
  const cleanups: (() => Promise<unknown>)[] = [() => rm(folder, { recursive: true, force: true })];
  const teardown = async () => {
    for (const cleanup of cleanups.reverse()) await cleanup().catch(() => undefined);
  };
  try {
    const collab = path.join(folder, "misty-collab");
    const fixtureBinary = path.join(folder, "collab-fixture");
    const builds = Promise.all([
      run("go", ["build", "-o", collab, "./cmd/misty-collab"]),
      run("go", ["build", "-o", fixtureBinary, "./test/collab/fixture"]),
    ]);
    const postgres = await startPostgres();
    cleanups.push(() => postgres.stop());
    await builds;
    const signingKey = randomBytes(32).toString("base64");
    const fixture = start(fixtureBinary, ["-addr", "127.0.0.1:0"], baseEnv({
      ...databaseEnv(postgres.app),
      MISTY_AUTH_SIGNING_KEY: signingKey,
    }));
    cleanups.push(() => fixture.stop());
    const listening = await fixture.waitFor(/fixture listening on (\S+)/);
    const fixtureUrl = `http://${listening[1]}`;
    const people = {} as Setup["people"];
    for (const username of ["ada", "ben", "cyd", "dee", "eve"] as const) {
      const response = await fetch(`${fixtureUrl}/users`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username }),
      });
      if (!response.ok) throw new Error(`creating ${username}: ${await response.text()}`);
      people[username] = (await response.json()) as Person;
    }
    project.provide("setup", { owner: postgres.owner, app: postgres.app, collab, signingKey, fixture: fixtureUrl, people });
  } catch (error) {
    await teardown();
    throw error;
  }
  return teardown;
}
