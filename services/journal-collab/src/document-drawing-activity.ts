import type * as Y from "yjs";

import { historyBytes } from "./document-history";
import { currentSnapshotBytes } from "./document-persistence";
import { drawingPlainText } from "./item-text";
import { postRoomCallback } from "./room-callback";
import type { Env } from "./room-env";

/**
 * Tells the Go API a drawing was just saved, so its "updated" time (Recently edited, the list's
 * order) follows the canvas: its id and a revision, the words of its text elements for search,
 * and what the room stores for storage. Never the scene.
 * Throws when the callback is rejected, so the caller can log and continue.
 */
export async function pushDrawingActivity(
  storage: DurableObjectStorage,
  env: Env,
  document: Y.Doc,
): Promise<void> {
  const drawingID = (await storage.get<string>("resourceID")) ?? "";
  if (!drawingID) return;
  const revision = ((await storage.get<number>("activityRevision")) ?? 0) + 1;
  await postRoomCallback(
    storage,
    env,
    "/internal/journal/drawing-activity",
    {
      drawing_id: drawingID,
      revision,
      plain_text: drawingPlainText(document),
      content_bytes: await currentSnapshotBytes(storage),
      history_bytes: await historyBytes(storage),
    },
    "drawing_activity_callback",
  );
  await storage.put("activityRevision", revision);
}
