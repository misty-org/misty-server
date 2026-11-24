import type { Person } from "./global_setup.js";
import type { Client } from "./sdk.js";
import type { Suite } from "./suite.js";
import { until } from "./wait.js";
import type { DriveItem } from "./world.js";

export type Kind = "text" | "app";

export type Writer = Record<string, unknown> & {
  writer?: boolean;
  dirty?: boolean;
  loaded?: boolean;
  read_only?: boolean;
  generation?: string;
  base?: number;
  renamed?: number;
  has_writer?: boolean;
  outside?: boolean;
  drive_version?: number;
  resolving?: number;
  kept?: boolean;
  kept_name?: string;
  kept_fingerprint?: string;
  fingerprint?: string;
};

export const writerOf = (c: Client): Writer | undefined => c.last("file_writer") as Writer | undefined;

export const writerSays = (c: Client, check: (said: Writer) => boolean, what: string, timeoutMs = 10_000) =>
  until(() => {
    const said = writerOf(c);
    return said !== undefined && check(said);
  }, [c.file.source], timeoutMs, what);

export const heard = (c: Client, type: string, check: (message: Record<string, unknown>) => boolean = () => true, timeoutMs = 10_000) =>
  until(() => c.file.some((message) => message.type === type && check(message)), [c.file.source], timeoutMs, `a ${type} message`);

export async function fileItem(s: Suite, label: string, kind: Kind, version?: number, by?: Person): Promise<DriveItem> {
  const item = await s.world.file(`${s.key(label)}.${kind === "text" ? "md" : "bin"}`);
  if (version !== undefined) await s.world.setVersions(item.id, version, version, by ?? s.world.people.ada);
  return item;
}

export function fileRoom(s: Suite, person: Person, item: DriveItem, kind: Kind, extra: Record<string, unknown> = {}) {
  return s.open(person, item.id, { options: kind === "app" ? { file: item.id, text: null } : { file: item.id }, ...extra });
}

export const loadText = (c: Client, text: string, version: number) =>
  c.doc.transact(() => {
    c.doc.getText("text").insert(0, text);
    c.doc.getMap("misty:file").set("loaded", version);
  });

export const loadApp = (c: Client, value: unknown, version: number) =>
  c.doc.transact(() => {
    c.doc.getMap("content").set("value", value);
    c.doc.getMap("misty:file").set("loaded", version);
  });
