import {
  isRecord,
  jsonResponse,
  verifyControlRequestWithRotation,
} from "./control_protocol";
import type { Env } from "./room_env";
import { MAX_RESOURCE_ID_LENGTH } from "./ticket";

const MAX_CONTROL_BODY_BYTES = 512 * 1024;

interface ControlCommand {
  command: string;
  payload: Record<string, unknown>;
  resourceID: unknown;
}

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

export async function bindControlResourceID(
  storage: DurableObjectStorage,
  resourceID: unknown,
): Promise<Response | null> {
  if (resourceID === undefined) return null;
  if (typeof resourceID !== "string" || !resourceID.trim() || resourceID.length > MAX_RESOURCE_ID_LENGTH) {
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
