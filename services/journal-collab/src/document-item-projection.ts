import type * as Y from "yjs";

import { historyBytes } from "./document-history";
import { currentSnapshotBytes } from "./document-persistence";
import {
  deckPlainText,
  deckSlideCount,
  deckTitle,
  workbookCellCount,
  workbookPlainText,
  workbookTitle,
} from "./item-text";
import { postRoomCallback } from "./room-callback";
import type { Env } from "./room-env";

export type MadeItemKind = "deck" | "workbook";

/**
 * Publishes a deck's or workbook's projection to the Go API after a save: its title, its plain
 * text for search (at most 100k characters), what the room stores, and its slide or filled-cell
 * count for the Space level's limit. The answer carries the limits back.
 * Throws when the callback is rejected, so the caller can log and continue.
 */
export async function pushItemProjection(
  storage: DurableObjectStorage,
  env: Env,
  document: Y.Doc,
  kind: MadeItemKind,
): Promise<void> {
  const itemID = (await storage.get<string>("resourceID")) ?? "";
  if (!itemID) return;
  const revision = ((await storage.get<number>("projectionRevision")) ?? 0) + 1;
  const deck = kind === "deck";
  await postRoomCallback(
    storage,
    env,
    `/internal/journal/${kind}-projections`,
    {
      item_id: itemID,
      revision,
      title: (deck ? deckTitle(document) : workbookTitle(document)).slice(0, 200),
      plain_text: deck ? deckPlainText(document) : workbookPlainText(document),
      content_bytes: await currentSnapshotBytes(storage),
      history_bytes: await historyBytes(storage),
      count: deck ? deckSlideCount(document) : workbookCellCount(document),
    },
    `${kind}_projection_callback`,
  );
  await storage.put("projectionRevision", revision);
}
