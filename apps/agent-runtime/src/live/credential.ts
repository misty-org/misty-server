import { controlPlaneURL } from "../control-plane.js";
import { ModelSetupError, modelRouteSchema, parseModelId, resolveTarget } from "../model-provider.js";
import { decodeControlSecret, signRequest, signatureHeaders } from "../signature.js";
import type { LiveTicket } from "./ticket.js";

/**
 * The Soniox key one live session runs on. A ticket can't carry a secret, so the relay asks Go
 * for the session's route over the signed control channel: the account's own Soniox key, or
 * `instance` for the runtime's `SONIOX_API_KEY`. The key lives only in the open socket's memory.
 */
export async function liveSonioxKey(ticket: LiveTicket, fetcher: typeof fetch = fetch, env: Record<string, string | undefined> = process.env): Promise<string> {
  if (parseModelId(ticket.model).provider !== "soniox")
    throw new ModelSetupError("model_unsupported", "Live transcription runs on Soniox models.");
  const path = "/internal/voice/live/credential";
  const body = Buffer.from(JSON.stringify({ session_id: ticket.sid, user: ticket.sub }));
  const timestamp = Math.floor(Date.now() / 1000).toString();
  const response = await fetcher(controlPlaneURL() + path, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      [signatureHeaders.timestamp]: timestamp,
      [signatureHeaders.signature]: signRequest(decodeControlSecret(), "POST", path, timestamp, body),
      [signatureHeaders.idempotency]: `voice-live-credential:${ticket.sid}`,
    },
    body,
    signal: AbortSignal.timeout(15_000),
  });
  // Neither a failure's body nor a malformed route is repeated: either may hold a key.
  if (!response.ok) throw new Error(`live_route_unavailable: ${response.status}`);
  const route = modelRouteSchema.safeParse(await response.json().catch(() => undefined));
  if (!route.success) throw new Error("invalid_model_route");
  return resolveTarget(ticket.model, route.data, env).credential.apiKey;
}
