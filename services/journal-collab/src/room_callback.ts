import { signServiceRequest } from "./control_protocol";
import type { Env } from "./room_env";
import { storeRoomLimits } from "./room_limits";

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
  }
  await storeRoomLimits(storage, answer);
  return answer;
}
