import { createHash, randomBytes } from "node:crypto";
import pg from "pg";
import { inject } from "vitest";
import type { Person } from "./global_setup.js";
import { Recorder, untilAsync } from "./wait.js";

export interface DriveItem {
  id: string;
  space_id: string;
  name: string;
  kind: string;
  sha256: string | null;
  size: number;
  mtime_ms: number;
  version: number;
  content_version: number;
  modified_by: string;
}

export interface Token {
  token: string;
  session: string;
  public_session: string;
}

export async function call<T>(path: string, body: Record<string, unknown>): Promise<T> {
  const response = await fetch(`${inject("setup").fixture}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const said = (await response.json()) as T & { error?: string };
  if (!response.ok) throw new Error(`${path}: ${said.error ?? response.status}`);
  return said;
}

let pool: pg.Pool | null = null;
let listener: pg.Client | null = null;
let listening: Promise<void> | null = null;
export const spaceEvents = new Recorder<string>();

function listen(): Promise<void> {
  if (listening) return listening;
  const { owner } = inject("setup");
  const client = new pg.Client({ host: owner.host, port: owner.port, user: owner.user, password: owner.password, database: owner.name });
  listener = client;
  client.on("notification", (message) => spaceEvents.push(message.payload ?? ""));
  listening = client.connect().then(() => client.query("LISTEN misty_space_events")).then(() => undefined);
  return listening;
}

export async function driveChanges(space: string, changed: (itemId: string) => void): Promise<() => void> {
  await listen();
  let last = Number((await database().query(`SELECT COALESCE(max(id),0) AS id FROM space_events`)).rows[0]?.id ?? 0);
  let reading: Promise<void> = Promise.resolve();
  let stopped = false;
  const stop = spaceEvents.source(() => {
    reading = reading.then(async () => {
      if (stopped || !pool) return;
      const rows = (
        await database().query(
          `SELECT id,entity_id FROM space_events WHERE id>$1 AND space_id=$2 AND event_type='drive.changed' ORDER BY id`,
          [last, space],
        )
      ).rows;
      for (const row of rows) {
        last = Math.max(last, Number(row.id));
        if (!stopped && row.entity_id) changed(String(row.entity_id));
      }
    }).catch(() => undefined);
  });
  return () => {
    stopped = true;
    stop();
  };
}

export async function driveUntil(check: () => Promise<boolean>, timeoutMs = 20_000, what = "the drive") {
  await listen();
  await untilAsync(check, [spaceEvents.source], timeoutMs, what);
}

export function database(): pg.Pool {
  if (pool) return pool;
  const { owner } = inject("setup");
  pool = new pg.Pool({ host: owner.host, port: owner.port, user: owner.user, password: owner.password, database: owner.name, max: 4 });
  return pool;
}

export async function closeDatabase() {
  const was = pool;
  const heard = listener;
  pool = null;
  listener = null;
  listening = null;
  await was?.end();
  await heard?.end();
}

export const sha256 = (text: string | Uint8Array) => createHash("sha256").update(text).digest("hex");

export const unique = (label: string) => `${label}-${randomBytes(5).toString("hex")}`;

export interface World {
  space: string;
  name: string;
  people: Record<"ada" | "ben" | "cyd" | "dee" | "eve", Person>;
  token(person: Person, options?: { session?: string; ttl?: number }): Promise<Token>;
  file(name: string, text?: string, by?: Person): Promise<DriveItem>;
  write(item: string, text: string, by?: Person): Promise<DriveItem>;
  item(id: string): Promise<DriveItem>;
  setVersions(id: string, version: number, content: number, by?: Person): Promise<void>;
  copies(of: DriveItem): Promise<DriveItem[]>;
  lockDrive(): Promise<() => Promise<void>>;
}

const ITEM_COLUMNS = `id,space_id,name,kind,sha256,byte_size AS size,mtime_ms,version,content_version,modified_by`;

const asItem = (row: Record<string, unknown>): DriveItem => ({
  ...(row as unknown as DriveItem),
  size: Number(row.size),
  mtime_ms: Number(row.mtime_ms),
  version: Number(row.version),
  content_version: Number(row.content_version),
});

export async function world(label = "Runtime"): Promise<World> {
  const { people } = inject("setup");
  const name = unique(label);
  const space = await call<{ id: string }>("/spaces", {
    owner: people.ada.id,
    members: [people.ben.id, people.cyd.id, people.dee.id],
    name,
  });
  const item = async (id: string) => {
    const result = await database().query(`SELECT ${ITEM_COLUMNS} FROM space_drive_items WHERE id=$1`, [id]);
    if (!result.rows[0]) throw new Error(`no drive item ${id}`);
    return asItem(result.rows[0]);
  };
  return {
    space: space.id,
    name,
    people,
    token: (person, options = {}) =>
      call<Token>("/tokens/person", {
        user: person.id,
        ...(options.session ? { session: options.session } : {}),
        ...(options.ttl ? { ttl_seconds: options.ttl } : {}),
      }),
    async file(fileName, text = "", by = people.ada) {
      const made = await call<{ id: string }>("/files", { user: by.id, space: space.id, name: fileName, text });
      return item(made.id);
    },
    async write(id, text, by = people.ada) {
      await call("/files/write", { user: by.id, space: space.id, item: id, text });
      return item(id);
    },
    item,
    async setVersions(id, version, content, by) {
      await database().query(
        `UPDATE space_drive_items SET version=$2,content_version=$3,modified_by=COALESCE($4,modified_by) WHERE id=$1`,
        [id, version, content, by?.id ?? null],
      );
    },
    async copies(of) {
      const dot = of.name.lastIndexOf(".");
      const stem = dot > 0 ? of.name.slice(0, dot) : of.name;
      const result = await database().query(
        `SELECT ${ITEM_COLUMNS} FROM space_drive_items WHERE space_id=$1 AND id<>$2 AND kind='file' AND deleted_at IS NULL
          AND starts_with(name, $3) ORDER BY seq`,
        [of.space_id, of.id, `${stem} (conflicted copy`],
      );
      return result.rows.map(asItem);
    },
    async lockDrive() {
      const client = await database().connect();
      await client.query("BEGIN");
      await client.query("SET LOCAL lock_timeout = '10s'");
      try {
        await client.query("LOCK TABLE space_drive_items IN ACCESS EXCLUSIVE MODE");
      } catch (error) {
        const held = await database().query(
          `SELECT a.pid,a.state,a.xact_start,left(a.query,120) AS query FROM pg_stat_activity a WHERE a.datname=current_database() AND a.state<>'idle'`,
        );
        await client.query("ROLLBACK").catch(() => undefined);
        client.release();
        throw new Error(`could not lock the drive: ${String(error)}\n${JSON.stringify(held.rows, null, 1)}`);
      }
      return async () => {
        await client.query("COMMIT");
        client.release();
      };
    },
  };
}
