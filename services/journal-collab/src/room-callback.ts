import { signServiceRequest } from "./control-protocol";
import type { Env } from "./room-env";
import { storeRoomLimits } from "./room-limits";

/**
 * Posts one signed callback to the Go API and returns its JSON answer. The answer's limits are
 * kept for the room. Throws when the callback is rejected, so the caller can log and continue.
 */
export async function postRoomCallback(
  storage: DurableObjectStorage,
  env: Env,
  path: string,
  payload: Record<string, unknown>,
  failure: string,
): Promise<unknown> {
  const bytes = new TextEncoder().encode(JSON.stringify(payload));
  const timestamp = String(Math.floor(Date.now() / 1000));
  const signature = await signServiceRequest(env.JOURNAL_COLLAB_PROJECTION_SECRET, timestamp, bytes);
  const response = await fetch(`${env.MISTY_INTERNAL_API_BASE.replace(/\/$/u, "")}${path}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Misty-Timestamp": timestamp,
      "X-Misty-Signature": signature,
    },
    body: bytes,
  });
  if (!response.ok) throw new Error(`${failure}_${response.status}`);
  let answer: unknown = null;
  try {
    answer = await response.json();
  } catch {
    // An API from before limits answered with nothing to keep.
  }
  await storeRoomLimits(storage, answer);
  return answer;
}
