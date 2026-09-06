import {
  decodeControlSecret,
  signRequest,
  signatureHeaders,
} from "./signature.js";
import { ControlPlaneError } from "./control-plane-error.js";

export interface RuntimeIdentity {
  mistyRunId: string;
  runtimeRunId: string;
  controlPlaneURL: string;
}

function normalizedBaseURL(value: string): string {
  const parsed = new URL(value);
  if (
    parsed.protocol !== "https:" &&
    !["localhost", "127.0.0.1", "api", "misty-api"].includes(parsed.hostname)
  ) {
    throw new Error(
      "MISTY_INTERNAL_API_BASE must use HTTPS except on the private local network",
    );
  }
  return parsed.toString().replace(/\/$/, "");
}

export function controlPlaneURL(callbackURL?: string): string {
  return normalizedBaseURL(
    callbackURL?.trim() ||
      process.env.MISTY_INTERNAL_API_BASE ||
      "http://api:8080",
  );
}

export async function controlPlaneRequest<T>(
  identity: RuntimeIdentity,
  action:
    | "activate"
    | "context"
    | "mcp-token"
    | "tools"
    | "events"
    | "complete",
  payload: Record<string, unknown>,
  idempotencyKey: string,
): Promise<T> {
  const path = `/internal/agent-runtime/runs/${encodeURIComponent(identity.mistyRunId)}/${action}`;
  const body = Buffer.from(
    JSON.stringify({ runtime_run_id: identity.runtimeRunId, ...payload }),
  );
  const timestamp = Math.floor(Date.now() / 1000).toString();
  const response = await fetch(identity.controlPlaneURL + path, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      [signatureHeaders.timestamp]: timestamp,
      [signatureHeaders.signature]: signRequest(
        decodeControlSecret(),
        "POST",
        path,
        timestamp,
        body,
      ),
      [signatureHeaders.idempotency]: idempotencyKey,
    },
    body,
    signal: AbortSignal.timeout(action === "tools" ? 5 * 60_000 : 30_000),
  });
  const text = await response.text();
  if (!response.ok) {
    let code = "control_plane_error";
    let detail = text.slice(0, 500);
    try {
      const payload = JSON.parse(text) as { code?: string; message?: string };
      code = payload.code?.trim() || code;
      detail = payload.message?.trim() || payload.code?.trim() || detail;
    } catch {
      // Non-JSON errors are still bounded before they enter user-visible logs.
    }
    throw new ControlPlaneError(
      response.status,
      `${code}: ${detail || `Misty control plane returned ${response.status}`}`,
      code,
    );
  }
  return (text ? JSON.parse(text) : {}) as T;
}
