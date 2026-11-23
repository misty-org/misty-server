import type * as Y from "yjs";

import { markdownToPlainText } from "./document_content";
import { historyBytes } from "./document_history";
import { currentSnapshotBytes } from "./document_persistence";
import { postRoomCallback } from "./room_callback";
import type { Env } from "./room_env";

export async function pushNoteProjection(
  storage: DurableObjectStorage,
  env: Env,
  document: Y.Doc,
): Promise<void> {
  const noteID = (await storage.get<string>("resourceID")) ?? "";
  if (!noteID) return;
  const title = document.getText("misty:title").toString().trim() || "Untitled doc";
  const markdown = document.getText("misty:markdown").toString();
  const metadata = document.getMap<unknown>("misty:document");
  const outgoing = Array.isArray(metadata.get("outgoing_note_ids"))
    ? (metadata.get("outgoing_note_ids") as unknown[]).filter((value): value is string => typeof value === "string")
    : [];
  const revision = ((await storage.get<number>("projectionRevision")) ?? 0) + 1;
  await postRoomCallback(
    storage,
    env,
    "/internal/journal/note-projections",
    {
      note_id: noteID,
      revision,
      title,
      markdown,
      plain_text: markdownToPlainText(markdown),
      outgoing_note_ids: outgoing,
      content_bytes: await currentSnapshotBytes(storage),
      history_bytes: await historyBytes(storage),
    },
    "projection_callback",
  );
  await storage.put("projectionRevision", revision);
}
