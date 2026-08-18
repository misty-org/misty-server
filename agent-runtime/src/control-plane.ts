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
    !["localhost", "127.0.0.1", "api"].includes(parsed.hostname)
  ) {
    throw new Error(
      "MISTY_INTERNAL_API_BASE must use HTTPS except on the private local network",
    );
  }
  return parsed.toString().replace(/\/$/, "");
}

export function controlPlaneURL(): string {
  return normalizedBaseURL(
    process.env.MISTY_INTERNAL_API_BASE ?? "http://api:8080",
  );
}

export async function controlPlaneRequest<T>(
  identity: RuntimeIdentity,
  action: "activate" | "context" | "tools" | "events" | "complete",
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
    signal: AbortSignal.timeout(30_000),
  });
  const text = await response.text();
  if (!response.ok)
    throw new ControlPlaneError(
      response.status,
      `Misty control plane returned ${response.status}: ${text.slice(0, 500)}`,
    );
  return (text ? JSON.parse(text) : {}) as T;
}
