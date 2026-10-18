import { controlPlaneURL } from "../control-plane.js";
import { decodeControlSecret, signRequest, signatureHeaders } from "../signature.js";

export type Settlement = { session_id: string; user: string; used_ms: number };

/**
 * Reports a closed live session's measured audio to the Go API, which settles
 * the billing reservation once. Retries briefly; the API ignores repeats.
 */
export async function settleLiveSession(settlement: Settlement, fetcher: typeof fetch = fetch): Promise<boolean> {
  const path = "/internal/voice/live/settle";
  const body = Buffer.from(JSON.stringify(settlement));
  for (let attempt = 0; attempt < 3; attempt++) {
    const timestamp = Math.floor(Date.now() / 1000).toString();
    try {
      const response = await fetcher(controlPlaneURL() + path, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          [signatureHeaders.timestamp]: timestamp,
          [signatureHeaders.signature]: signRequest(decodeControlSecret(), "POST", path, timestamp, body),
          [signatureHeaders.idempotency]: `voice-live-settle:${settlement.session_id}`,
        },
        body,
        signal: AbortSignal.timeout(15_000),
      });
      if (response.ok) return true;
      if (response.status < 500) return false;
    } catch {
      // Network failure: try again.
    }
    await new Promise((resolve) => setTimeout(resolve, 500 * 2 ** attempt));
  }
  return false;
}
