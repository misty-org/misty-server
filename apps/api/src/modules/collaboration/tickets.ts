import { createHmac, randomUUID } from "node:crypto";
import { SignJWT } from "jose";
import type { CollaborationConfig } from "./config.js";

export type ResourceKind = "note" | "drawing";
export function collaborationRoom(config: Pick<CollaborationConfig, "roomSalt">, kind: ResourceKind, id: string) {
  return createHmac("sha256", config.roomSalt).update(`${kind}-room:${id}`).digest("base64url").slice(0, 32);
}
export function createCollaborationTickets(config: CollaborationConfig, clock = () => new Date()) {
  return async (input: { userId: string; spaceId: string; kind: ResourceKind; id: string; role: "creator" | "editor" | "viewer"; aclVersion: number }, exportRead = false) => {
    if (!input.userId || !input.spaceId || !input.id || !Number.isSafeInteger(input.aclVersion) || input.aclVersion < 1) throw new Error("Invalid collaboration identity");
    const room = collaborationRoom(config, input.kind, input.id), role = exportRead ? "viewer" : input.role;
    const expiry = Math.floor(clock().getTime() / 1000) + (exportRead ? 900 : 60);
    const ticket = await new SignJWT({ space_id: input.spaceId, resource_type: input.kind, resource_id: input.id,
      [input.kind === "note" ? "note_id" : "drawing_id"]: input.id, room, role, acl_version: input.aclVersion })
      .setProtectedHeader({ alg: "EdDSA", typ: "JWT" }).setIssuer(config.issuer).setAudience(config.audience)
      .setSubject(input.userId).setJti(`tkt_${randomUUID()}`).setExpirationTime(expiry).sign(config.privateKey);
    return { ticket, room, url: `${config.origin.replace(/^http/, "ws")}/parties/${input.kind}-room/${room}`, role, expires_at: new Date(expiry * 1000).toISOString() };
  };
}
