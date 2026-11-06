import type * as Y from "yjs";

import { signServiceRequest } from "./control-protocol";
import { markdownToPlainText } from "./document-content";
import type { Env } from "./room-env";

/**
 * Publishes the note's current title and markdown to the Go API.
 * Throws when the callback is rejected, so the caller can log and continue.
 */
export async function pushNoteProjection(
  storage: DurableObjectStorage,
  env: Env,
  document: Y.Doc,
): Promise<void> {
  const noteID = (await storage.get<string>("resourceID")) ?? "";
  if (!noteID) return;
  const title = document.getText("misty:title").toString().trim() || "Untitled note";
  const markdown = document.getText("misty:markdown").toString();
  const metadata = document.getMap<unknown>("misty:document");
  const outgoing = Array.isArray(metadata.get("outgoing_note_ids"))
    ? (metadata.get("outgoing_note_ids") as unknown[]).filter((value): value is string => typeof value === "string")
    : [];
  const revision = ((await storage.get<number>("projectionRevision")) ?? 0) + 1;
  const payload = JSON.stringify({
    note_id: noteID,
    revision,
    title,
    markdown,
    plain_text: markdownToPlainText(markdown),
    outgoing_note_ids: outgoing,
  });
  const bytes = new TextEncoder().encode(payload);
  const timestamp = String(Math.floor(Date.now() / 1000));
  const signature = await signServiceRequest(env.JOURNAL_COLLAB_PROJECTION_SECRET, timestamp, bytes);
  const response = await fetch(`${env.MISTY_INTERNAL_API_BASE.replace(/\/$/u, "")}/internal/journal/note-projections`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Misty-Timestamp": timestamp, "X-Misty-Signature": signature },
    body: bytes,
  });
  if (!response.ok) throw new Error(`projection_callback_${response.status}`);
  await storage.put("projectionRevision", revision);
}
