import { signServiceRequest } from "./control-protocol";
import type { Env } from "./room-env";

/**
 * Tells the Go API a drawing was just saved, so its "updated" time (Recently edited, the list's
 * order) follows the canvas. Only the id and a revision go out, never the scene.
 * Throws when the callback is rejected, so the caller can log and continue.
 */
export async function pushDrawingActivity(storage: DurableObjectStorage, env: Env): Promise<void> {
  const drawingID = (await storage.get<string>("resourceID")) ?? "";
  if (!drawingID) return;
  const revision = ((await storage.get<number>("activityRevision")) ?? 0) + 1;
  const bytes = new TextEncoder().encode(JSON.stringify({ drawing_id: drawingID, revision }));
  const timestamp = String(Math.floor(Date.now() / 1000));
  const signature = await signServiceRequest(env.JOURNAL_COLLAB_PROJECTION_SECRET, timestamp, bytes);
  const response = await fetch(
    `${env.MISTY_INTERNAL_API_BASE.replace(/\/$/u, "")}/internal/journal/drawing-activity`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Misty-Timestamp": timestamp,
        "X-Misty-Signature": signature,
      },
      body: bytes,
    },
  );
  if (!response.ok) throw new Error(`drawing_activity_callback_${response.status}`);
  await storage.put("activityRevision", revision);
}
