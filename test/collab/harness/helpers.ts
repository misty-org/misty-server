import * as Y from "yjs";
import type { Client } from "./sdk.js";
import { onAwareness, onDoc, until } from "./wait.js";
import { database } from "./world.js";

export const statuses = (c: Client, ...kinds: string[]) =>
  c.messages.items.filter((message) => message.type === "document_status" && kinds.includes(String(message.status))).length;

export async function saved(c: Client, edit: () => void, timeoutMs = 20_000) {
  const before = statuses(c, "saved", "warning");
  edit();
  await until(() => statuses(c, "saved", "warning") > before, [c.messages.source], timeoutMs, "the room to save");
}

export const closedWith = (c: Client, code: number, timeoutMs = 15_000) =>
  until(() => c.closes.some((close) => close.code === code), [c.closes.source], timeoutMs, `a close with ${code}`);

export const textIs = (c: Client, name: string, text: string, timeoutMs = 15_000) =>
  until(() => c.doc.getText(name).toString() === text, [onDoc(c.doc)], timeoutMs, `${name} to read ${JSON.stringify(text)}`);

type State = Record<string, unknown> & { misty?: { id?: string; name?: string }; user?: { name?: string } };

export const states = (c: Client): State[] => [...c.room.awareness.getStates().values()] as State[];

export const sees = (c: Client, id: string, name?: string) =>
  states(c).some((state) => state.misty?.id === id && (name === undefined || state.user?.name === name));

export const presence = (c: Client, check: () => boolean, what: string, timeoutMs = 10_000) =>
  until(check, [onAwareness(c.room.awareness)], timeoutMs, what);

export async function notify(payload: Record<string, unknown>) {
  await database().query(`SELECT pg_notify('misty_space_control', $1)`, [JSON.stringify(payload)]);
}

export async function storedDoc(roomId: string): Promise<Y.Doc | null> {
  const result = await database().query(`SELECT state FROM collab_documents WHERE room_id=$1`, [roomId]);
  if (!result.rows[0]) return null;
  const doc = new Y.Doc({ gc: false });
  Y.applyUpdate(doc, new Uint8Array(result.rows[0].state as Buffer));
  return doc;
}

export const docWith = (doc: Y.Doc, check: () => boolean, what: string, timeoutMs = 15_000) =>
  until(check, [onDoc(doc)], timeoutMs, what);
