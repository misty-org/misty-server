import {
  isRecord,
  jsonResponse,
  verifyControlRequestWithRotation,
} from "./control-protocol";
import type { Env } from "./room-env";

/** Scene mutations are bounded below the Yjs document ceiling. */
const MAX_CONTROL_BODY_BYTES = 512 * 1024;

export interface ControlCommand {
  command: string;
  payload: Record<string, unknown>;
  resourceID: unknown;
}

/**
 * Verifies a signed service request and parses its command envelope.
 * Returns the error response to send, or the parsed command.
 *
 * The timestamp is part of the signature and has a short acceptance window,
 * so a captured request cannot be replayed later. The command body is signed
 * byte-for-byte before JSON parsing.
 */
export async function parseControlRequest(
  request: Request,
  env: Env,
): Promise<Response | ControlCommand> {
  const body = new Uint8Array(await request.arrayBuffer());
  if (body.byteLength > MAX_CONTROL_BODY_BYTES) {
    return jsonResponse({ code: "body_too_large" }, 413);
  }
  const timestamp = request.headers.get("X-Misty-Timestamp") ?? "";
  const signature = request.headers.get("X-Misty-Signature") ?? "";
  if (
    !(await verifyControlRequestWithRotation(
      env.JOURNAL_COLLAB_CONTROL_SECRET,
      env.JOURNAL_COLLAB_CONTROL_SECRET_PREVIOUS,
      timestamp,
      body,
      signature,
    ))
  ) {
    return jsonResponse({ code: "unauthorized" }, 401);
  }

  let envelope: unknown;
  try {
    envelope = JSON.parse(new TextDecoder().decode(body));
  } catch {
    return jsonResponse({ code: "invalid_json" }, 400);
  }
  if (
    !isRecord(envelope) ||
    typeof envelope.command !== "string" ||
    !isRecord(envelope.payload)
  ) {
    return jsonResponse({ code: "invalid_command" }, 400);
  }
  return {
    command: envelope.command,
    payload: envelope.payload,
    resourceID: envelope.resource_id,
  };
}

/**
 * Binds the room to the resource id a signed service request carries.
 * Agent-created notes may be initialized before any editor opens a socket.
 * Older service requests remain compatible, but cannot change a binding.
 * Returns an error response to send, or null when the request may continue.
 */
export async function bindControlResourceID(
  storage: DurableObjectStorage,
  resourceID: unknown,
): Promise<Response | null> {
  if (resourceID === undefined) return null;
  if (typeof resourceID !== "string" || !resourceID.trim() || resourceID.length > 200) {
    return jsonResponse({ code: "invalid_resource_id" }, 400);
  }
  const trimmed = resourceID.trim();
  const existing = await storage.get<string>("resourceID");
  if (existing && existing !== trimmed) {
    return jsonResponse({ code: "resource_mismatch" }, 409);
  }
  await storage.put("resourceID", trimmed);
  return null;
}
