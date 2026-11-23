import * as Y from "yjs";

import type { CollaborationResourceType } from "./ticket";

type Shape = "map" | "array" | "text" | "xml";

const KNOWN_SHAPES: Record<string, Shape> = {
  "misty:title": "text",
  "misty:markdown": "text",
  "misty:document": "map",
};

const NOTE_BOOKKEEPING = new Set(["pending_version", "applied_version"]);

const RESTORE_ORIGIN = "misty:history-restore";

function shapeOf(type: Y.AbstractType<any> | undefined): Shape | null {
  if (!type) return null;
  if (type instanceof Y.Map) return "map";
  if (type instanceof Y.Array) return "array";
  if (type instanceof Y.XmlFragment) return "xml";
  if (type instanceof Y.Text) return "text";
  if (type._map.size > 0) {
    for (const item of type._map.values()) if (!item.deleted) return "map";
  }
  for (let item = type._start; item !== null; item = item.right) {
    if (item.deleted) continue;
    const content = item.content;
    if (content instanceof Y.ContentString || content instanceof Y.ContentFormat || content instanceof Y.ContentEmbed) {
      return "text";
    }
    if (
      content instanceof Y.ContentType &&
      (content.type instanceof Y.XmlElement || content.type instanceof Y.XmlText || content.type instanceof Y.XmlHook)
    ) {
      return "xml";
    }
    return "array";
  }
  return null;
}

function cloneValue(value: unknown): unknown {
  return value instanceof Y.AbstractType ? (value as unknown as { clone(): unknown }).clone() : value;
}

function sameJSON(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

function restoreMap(target: Y.Map<unknown>, source: Y.Map<unknown>, keep: ReadonlySet<string> = new Set()): void {
  for (const key of Array.from(target.keys())) {
    if (!keep.has(key) && !source.has(key)) target.delete(key);
  }
  for (const [key, value] of source.entries()) {
    if (keep.has(key)) continue;
    const current = target.get(key);
    if (!(value instanceof Y.AbstractType) && !(current instanceof Y.AbstractType) && sameJSON(value, current)) continue;
    target.set(key, cloneValue(value));
  }
}

export function restoreDocument(
  target: Y.Doc,
  update: Uint8Array,
  resourceType: CollaborationResourceType,
): void {
  const source = new Y.Doc();
  try {
    Y.applyUpdate(source, update);
    const names = new Set([...source.share.keys(), ...target.share.keys()]);
    target.transact(() => {
      for (const name of names) {
        const shape =
          KNOWN_SHAPES[name] ??
          shapeOf(target.share.get(name)) ??
          shapeOf(source.share.get(name));
        if (!shape) continue;
        switch (shape) {
          case "map": {
            const into = target.getMap<unknown>(name);
            const from = source.getMap<unknown>(name);
            if (resourceType === "note" && name === "misty:document") restoreMap(into, from, NOTE_BOOKKEEPING);
            else restoreMap(into, from);
            break;
          }
          case "array": {
            const into = target.getArray<unknown>(name);
            into.delete(0, into.length);
            into.insert(0, source.getArray<unknown>(name).toArray().map(cloneValue));
            break;
          }
          case "text": {
            const into = target.getText(name);
            into.delete(0, into.length);
            into.applyDelta(source.getText(name).toDelta());
            break;
          }
          case "xml": {
            const into = target.getXmlFragment(name);
            into.delete(0, into.length);
            into.insert(
              0,
              source
                .getXmlFragment(name)
                .toArray()
                .map((node) => node.clone() as Y.XmlElement | Y.XmlText),
            );
            break;
          }
        }
      }
    }, RESTORE_ORIGIN);
  } finally {
    source.destroy();
  }
}
