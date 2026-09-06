import { createHmac } from "node:crypto";
import type { CollaborationConfig } from "./config.js";
import { collaborationRoom, type ResourceKind } from "./tickets.js";

export type ControlSender = (kind: ResourceKind, id: string, command: string, payload: unknown) => Promise<void>;
export function createControlSender(config: CollaborationConfig, deployment: "hosted" | "self_hosted", transport: typeof fetch = fetch): ControlSender {
  return async (kind, id, command, payload) => {
    if (!["acl", "disconnect", "purge", "bootstrap"].includes(command) || !payload || typeof payload !== "object" || Array.isArray(payload)) throw new Error("Invalid collaboration control command");
    if (command === "acl" && !("acl_version" in payload && Number.isSafeInteger(payload.acl_version) && Number(payload.acl_version) >= 1)) throw new Error("Invalid collaboration ACL version");
    if (command === "bootstrap" && (kind !== "note" || !("title" in payload) || typeof payload.title !== "string" || !payload.title.trim() || payload.title.trim().length > 500 ||
      !("markdown" in payload) || typeof payload.markdown !== "string" || !payload.markdown.trim() || payload.markdown.trim().length > 100000)) throw new Error("Invalid collaboration bootstrap");
    const body = JSON.stringify({ command, payload });
    if (Buffer.byteLength(body) > 512 * 1024) throw new Error("Collaboration control command too large");
    const timestamp = String(Math.floor(Date.now() / 1000));
    const headers = { "Content-Type": "application/json", "X-Misty-Timestamp": timestamp,
      "X-Misty-Signature": createHmac("sha256", config.controlSecret).update(`${timestamp}\n${body}`).digest("base64url"),
      ...(deployment === "self_hosted" ? { "X-Misty-Resource-ID": id } : {}) };
    const response = await transport(`${config.origin}/parties/${kind}-room/${collaborationRoom(config, kind, id)}`, {
      method: "POST", headers, body, redirect: "error", signal: AbortSignal.timeout(10000),
    });
    const reader = response.body?.getReader(), chunks: Uint8Array[] = []; let length = 0;
    if (reader) try {
      for (;;) {
        const next = await reader.read(); if (next.done) break;
        length += next.value.byteLength;
        if (length > 65536) throw new Error("Collaboration control response too large");
        chunks.push(next.value);
      }
    } finally { await reader.cancel().catch(() => {}); }
    const result = JSON.parse(Buffer.concat(chunks).toString("utf8"));
    if (response.status === 410 && result?.code === "resource_deleted") return;
    if (!response.ok || result?.ok !== true || command === "purge" && result.purged !== true ||
      command === "acl" && !(Number.isSafeInteger(result.acl_version) && result.acl_version >= (payload as { acl_version: number }).acl_version)) throw new Error("Collaboration control was not acknowledged");
  };
}
