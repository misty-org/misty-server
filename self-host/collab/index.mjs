import {
  createHash,
  createHmac,
  createPublicKey,
  timingSafeEqual,
  verify as verifySignature,
} from "node:crypto";
import { createServer } from "node:http";
import { WebSocketServer } from "ws";
import * as decoding from "lib0/decoding";
import * as encoding from "lib0/encoding";
import * as awarenessProtocol from "y-protocols/awareness";
import * as syncProtocol from "y-protocols/sync";
import * as Y from "yjs";

const port = Number(process.env.PORT || 1999);
const apiBase = String(process.env.MISTY_INTERNAL_API_BASE || "http://api:8080").replace(/\/$/, "");
const internalSecret = String(process.env.MISTY_COLLAB_INTERNAL_SECRET || "");
const controlSecret = loadControlSecret(process.env.JOURNAL_COLLAB_CONTROL_SECRET || "");
const projectionSecret = loadControlSecret(process.env.JOURNAL_COLLAB_PROJECTION_SECRET || "");
const roomSalt = loadControlSecret(process.env.JOURNAL_COLLAB_ROOM_SALT || "");
const issuer = "misty-api";
const audience = "misty-journal-collab";
const publicKey = loadPublicKey(process.env.JOURNAL_COLLAB_TICKET_PUBLIC_KEY || "");
const maxDocumentBytes = 8 * 1024 * 1024;
const maxMessageBytes = 512 * 1024;
const messageSync = 0;
const messageAwareness = 1;
const rooms = new Map();
const usedTicketIDs = new Map();

const server = createServer((request, response) => {
  void handleHTTPRequest(request, response).catch((error) => {
    const code = error instanceof Error ? error.message : "internal_error";
    if (code === "request_too_large") return writeJSON(response, 413, { code });
    if (code === "stale_control" || code === "invalid_control_signature") return writeJSON(response, 401, { code });
    if (error instanceof SyntaxError || code === "invalid_resource" || code === "room_mismatch") {
      return writeJSON(response, 400, { code: code === "room_mismatch" ? code : "invalid_request" });
    }
    return writeJSON(response, 500, { code: "internal_error" });
  });
});
const sockets = new WebSocketServer({ noServer: true, maxPayload: maxMessageBytes });

server.on("upgrade", async (request, socket, head) => {
  try {
    const url = new URL(request.url || "/", "http://collab.internal");
    const match = url.pathname.match(/^\/parties\/(note-room|drawing-room)\/([A-Za-z0-9_-]{1,128})$/);
    if (!match) throw new Error("not_found");
    const resourceType = match[1] === "note-room" ? "note" : "drawing";
    const roomName = match[2];
    const claims = verifyTicket(url.searchParams.get("ticket") || "", roomName, resourceType);
    burnTicket(claims.jti, claims.exp);
    const room = await getRoom(roomName, resourceType, claims.resource_id);
    if (claims.acl_version < room.aclVersion) throw new Error("ticket_acl_stale");
    room.aclVersion = Math.max(room.aclVersion, claims.acl_version);
    sockets.handleUpgrade(request, socket, head, (websocket) => {
      attach(room, websocket, claims);
    });
  } catch {
    socket.write("HTTP/1.1 401 Unauthorized\r\nConnection: close\r\n\r\n");
    socket.destroy();
  }
});

server.listen(port, "0.0.0.0");

async function getRoom(name, resourceType, resourceID) {
  const existing = rooms.get(name);
  if (existing) {
    const room = await existing;
    if (room.resourceType !== resourceType || room.resourceID !== resourceID) throw new Error("room_mismatch");
    return room;
  }
  const pending = createRoom(name, resourceType, resourceID);
  rooms.set(name, pending);
  try {
    return await pending;
  } catch (error) {
    rooms.delete(name);
    throw error;
  }
}

async function createRoom(name, resourceType, resourceID) {
  const doc = new Y.Doc();
  const awareness = new awarenessProtocol.Awareness(doc);
  awareness.setLocalState(null);
  const room = { name, resourceType, resourceID, doc, awareness, clients: new Set(), aclVersion: 0, persistTimer: null };
  const response = await fetch(`${apiBase}/internal/self-host/collaboration/${resourceType}/${encodeURIComponent(resourceID)}`, {
    headers: { "X-Misty-Internal-Secret": internalSecret },
  });
  if (response.ok) {
    const update = new Uint8Array(await response.arrayBuffer());
    const checksum = createHash("sha256").update(update).digest("hex");
    if (checksum !== response.headers.get("X-Content-SHA256")) throw new Error("snapshot_checksum_mismatch");
    Y.applyUpdate(doc, update, "persistence");
    room.aclVersion = readNonNegativeInteger(response.headers.get("X-Misty-ACL-Version"), 0);
  } else if (response.status !== 404) {
    throw new Error("snapshot_unavailable");
  }
  doc.on("update", (update, origin) => {
    if (origin !== "persistence") broadcastUpdate(room, update, origin);
    schedulePersistence(room);
  });
  awareness.on("update", ({ added, updated, removed }, origin) => {
    const changed = added.concat(updated, removed);
    if (origin?.controlledAwarenessIDs) {
      for (const id of added.concat(updated)) origin.controlledAwarenessIDs.add(id);
      for (const id of removed) origin.controlledAwarenessIDs.delete(id);
    }
    const encoder = encoding.createEncoder();
    encoding.writeVarUint(encoder, messageAwareness);
    encoding.writeVarUint8Array(encoder, awarenessProtocol.encodeAwarenessUpdate(awareness, changed));
    broadcast(room, encoding.toUint8Array(encoder));
  });
  return room;
}

function attach(room, socket, claims) {
  socket.binaryType = "arraybuffer";
  socket.claims = claims;
  socket.controlledAwarenessIDs = new Set();
  room.clients.add(socket);
  socket.on("message", (data, binary) => {
    if (!binary) return;
    const bytes = new Uint8Array(data);
    if (bytes.byteLength > maxMessageBytes) return socket.close(1009, "message_too_large");
    try {
      handleMessage(room, socket, bytes);
    } catch {
      socket.close(1003, "invalid_message");
    }
  });
  socket.on("close", () => {
    room.clients.delete(socket);
    awarenessProtocol.removeAwarenessStates(room.awareness, [...socket.controlledAwarenessIDs], socket);
    schedulePersistence(room, true);
  });
  const syncEncoder = encoding.createEncoder();
  encoding.writeVarUint(syncEncoder, messageSync);
  syncProtocol.writeSyncStep1(syncEncoder, room.doc);
  send(socket, encoding.toUint8Array(syncEncoder));
  const awarenessClients = [...room.awareness.getStates().keys()];
  if (awarenessClients.length) {
    const awarenessEncoder = encoding.createEncoder();
    encoding.writeVarUint(awarenessEncoder, messageAwareness);
    encoding.writeVarUint8Array(awarenessEncoder, awarenessProtocol.encodeAwarenessUpdate(room.awareness, awarenessClients));
    send(socket, encoding.toUint8Array(awarenessEncoder));
  }
}

function handleMessage(room, socket, bytes) {
  const decoder = decoding.createDecoder(bytes);
  const type = decoding.readVarUint(decoder);
  if (type === messageSync) {
    const position = decoder.pos;
    const syncType = decoding.readVarUint(decoder);
    if (socket.claims.role === "viewer" && syncType !== 0) return;
    if (syncType === 1 || syncType === 2) {
      const update = decoding.readVarUint8Array(decoder);
      const projected = new Y.Doc();
      Y.applyUpdate(projected, Y.encodeStateAsUpdate(room.doc));
      Y.applyUpdate(projected, update);
      const size = Y.encodeStateAsUpdate(projected).byteLength;
      projected.destroy();
      if (size > maxDocumentBytes) return;
    }
    decoder.pos = position;
    const encoder = encoding.createEncoder();
    encoding.writeVarUint(encoder, messageSync);
    syncProtocol.readSyncMessage(decoder, encoder, room.doc, socket);
    if (encoding.length(encoder) > 1) send(socket, encoding.toUint8Array(encoder));
  } else if (type === messageAwareness) {
    awarenessProtocol.applyAwarenessUpdate(room.awareness, decoding.readVarUint8Array(decoder), socket);
  }
}

function broadcastUpdate(room, update, origin) {
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, messageSync);
  syncProtocol.writeUpdate(encoder, update);
  const message = encoding.toUint8Array(encoder);
  for (const client of room.clients) if (client !== origin) send(client, message);
}

function broadcast(room, message) {
  for (const client of room.clients) send(client, message);
}

function send(socket, message) {
  if (socket.readyState === 1) socket.send(message, { binary: true });
}

function schedulePersistence(room, immediate = false) {
  if (room.persistTimer) clearTimeout(room.persistTimer);
  room.persistTimer = setTimeout(() => void persist(room), immediate ? 0 : 2000);
}

async function persist(room) {
  if (room.persistTimer) clearTimeout(room.persistTimer);
  room.persistTimer = null;
  const update = Y.encodeStateAsUpdate(room.doc);
  if (update.byteLength > maxDocumentBytes) return;
  const checksum = createHash("sha256").update(update).digest("hex");
  const response = await fetch(`${apiBase}/internal/self-host/collaboration/${room.resourceType}/${encodeURIComponent(room.resourceID)}`, {
    method: "PUT",
    headers: {
      "Content-Type": "application/vnd.yjs.update",
      "X-Content-SHA256": checksum,
      "X-Misty-ACL-Version": String(room.aclVersion),
      "X-Misty-Internal-Secret": internalSecret,
    },
    body: update,
  }).catch(() => null);
  if (!response?.ok) {
    schedulePersistence(room);
  } else if (room.resourceType === "note") {
    await publishNoteProjection(room).catch(() => schedulePersistence(room));
  }
}

async function handleHTTPRequest(request, response) {
  if (request.method === "GET" && request.url === "/health") {
    return writeJSON(response, 200, { status: "ok" });
  }
  const url = new URL(request.url || "/", "http://collab.internal");
  const match = url.pathname.match(/^\/parties\/(note-room|drawing-room)\/([A-Za-z0-9_-]{1,128})$/);
  if (request.method !== "POST" || !match) return writeJSON(response, 404, { code: "not_found" });

  const body = await readRequestBody(request, 128 * 1024);
  verifyControlRequest(request, body);
  const envelope = JSON.parse(body.toString("utf8"));
  const resourceID = String(request.headers["x-misty-resource-id"] || "");
  if (!/^[A-Za-z0-9_-]{1,200}$/.test(resourceID)) throw new Error("invalid_resource");
  if (!envelope || typeof envelope.command !== "string" || typeof envelope.payload !== "object") {
    return writeJSON(response, 400, { code: "invalid_command" });
  }
  const resourceType = match[1] === "note-room" ? "note" : "drawing";
  const expectedRoom = createHmac("sha256", roomSalt)
    .update(`${resourceType}-room:${resourceID}`)
    .digest("base64url")
    .slice(0, 32);
  if (expectedRoom !== match[2]) throw new Error("room_mismatch");
  const room = await getRoom(match[2], resourceType, resourceID);
  return handleControl(response, room, envelope.command, envelope.payload || {});
}

async function handleControl(response, room, command, payload) {
  if (command === "bootstrap") {
    const title = typeof payload.title === "string" ? payload.title.trim() : "";
    const markdown = typeof payload.markdown === "string" ? payload.markdown.trim() : "";
    if (room.resourceType !== "note" || !title || title.length > 500 || !markdown || markdown.length > 100_000) {
      return writeJSON(response, 400, { code: "invalid_bootstrap" });
    }
    const initialized = room.doc.share.size === 0;
    if (initialized) {
      room.doc.transact(() => {
        const metadata = room.doc.getMap("misty:document");
        metadata.set("schema", "tiptap-v1");
        metadata.set("pending_version", 1);
        metadata.set("pending_markdown", markdown);
        replaceText(room.doc.getText("misty:title"), title);
        replaceText(room.doc.getText("misty:markdown"), markdown);
      });
      await persist(room);
    }
    return writeJSON(response, 200, { ok: true, initialized });
  }
  if (command === "replace_markdown") {
    const title = typeof payload.title === "string" ? payload.title.trim() : "";
    const markdown = typeof payload.markdown === "string" ? payload.markdown.trim() : "";
    if (room.resourceType !== "note" || !title || title.length > 500 || markdown.length > 100_000) {
      return writeJSON(response, 400, { code: "invalid_note_content" });
    }
    room.doc.transact(() => {
      const metadata = room.doc.getMap("misty:document");
      const version = Number(metadata.get("pending_version") || 0) + 1;
      metadata.set("schema", "tiptap-v1");
      metadata.set("pending_version", version);
      metadata.set("pending_markdown", markdown);
      replaceText(room.doc.getText("misty:title"), title);
      replaceText(room.doc.getText("misty:markdown"), markdown);
    });
    await persist(room);
    return writeJSON(response, 200, { ok: true });
  }
  if (command === "acl") {
    const version = Number(payload.acl_version);
    if (!Number.isInteger(version) || version < 1) return writeJSON(response, 400, { code: "invalid_acl_version" });
    room.aclVersion = Math.max(room.aclVersion, version);
    for (const client of room.clients) {
      if (client.claims.acl_version < room.aclVersion) client.close(1008, "acl_superseded");
    }
    await persist(room);
    return writeJSON(response, 200, { ok: true, acl_version: room.aclVersion });
  }
  if (command === "disconnect") {
    const userIDs = Array.isArray(payload.user_ids) ? new Set(payload.user_ids.filter((id) => typeof id === "string")) : null;
    for (const client of room.clients) {
      if (userIDs === null || userIDs.has(client.claims.sub)) client.close(1008, "access_revoked");
    }
    return writeJSON(response, 200, { ok: true });
  }
  if (command === "status") {
    return writeJSON(response, 200, {
      ok: true,
      acl_version: room.aclVersion,
      document_bytes: Y.encodeStateAsUpdate(room.doc).byteLength,
    });
  }
  if (command === "purge") {
    for (const client of room.clients) client.close(1008, "resource_purged");
    if (room.persistTimer) clearTimeout(room.persistTimer);
    room.persistTimer = null;
    const deleted = await fetch(`${apiBase}/internal/self-host/collaboration/${room.resourceType}/${encodeURIComponent(room.resourceID)}`, {
      method: "DELETE",
      headers: { "X-Misty-Internal-Secret": internalSecret },
    });
    if (!deleted.ok) throw new Error("purge_failed");
    rooms.delete(room.name);
    room.doc.destroy();
    return writeJSON(response, 200, { ok: true, purged: true });
  }
  return writeJSON(response, 400, { code: "unknown_command" });
}

function verifyControlRequest(request, body) {
  const timestamp = String(request.headers["x-misty-timestamp"] || "");
  const signature = String(request.headers["x-misty-signature"] || "");
  const seconds = Number(timestamp);
  if (!Number.isInteger(seconds) || Math.abs(Math.floor(Date.now() / 1000) - seconds) > 300) throw new Error("stale_control");
  const expected = createHmac("sha256", controlSecret).update(timestamp).update("\n").update(body).digest("base64url");
  const actualBuffer = Buffer.from(signature);
  const expectedBuffer = Buffer.from(expected);
  if (actualBuffer.length !== expectedBuffer.length || !timingSafeEqual(actualBuffer, expectedBuffer)) throw new Error("invalid_control_signature");
}

function replaceText(text, value) {
  text.delete(0, text.length);
  if (value) text.insert(0, value);
}

async function publishNoteProjection(room) {
  const metadata = room.doc.getMap("misty:document");
  const markdown = room.doc.getText("misty:markdown").toString();
  const outgoing = Array.isArray(metadata.get("outgoing_note_ids"))
    ? metadata.get("outgoing_note_ids").filter((value) => typeof value === "string")
    : [];
  const body = Buffer.from(JSON.stringify({
    note_id: room.resourceID,
    revision: Date.now(),
    title: room.doc.getText("misty:title").toString().trim() || "Untitled note",
    markdown,
    plain_text: markdown.replace(/!\[[^\]]*\]\([^)]*\)/gu, " ").replace(/\[([^\]]+)\]\([^)]*\)/gu, "$1").replace(/[`*_>#~-]+/gu, " ").replace(/\s+/gu, " ").trim(),
    outgoing_note_ids: outgoing,
  }));
  const timestamp = String(Math.floor(Date.now() / 1000));
  const signature = createHmac("sha256", projectionSecret).update(timestamp).update("\n").update(body).digest("base64url");
  const response = await fetch(`${apiBase}/internal/journal/note-projections`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Misty-Timestamp": timestamp, "X-Misty-Signature": signature },
    body,
  });
  if (!response.ok) throw new Error("projection_failed");
}

function readRequestBody(request, limit) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    request.on("data", (chunk) => {
      size += chunk.length;
      if (size > limit) {
        reject(new Error("request_too_large"));
        request.destroy();
      } else chunks.push(chunk);
    });
    request.on("end", () => resolve(Buffer.concat(chunks)));
    request.on("error", reject);
  });
}

function writeJSON(response, status, payload) {
  if (response.headersSent) return;
  response.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store" });
  response.end(JSON.stringify(payload));
}

function readNonNegativeInteger(raw, fallback) {
  const value = Number(raw);
  return Number.isInteger(value) && value >= 0 ? value : fallback;
}

function verifyTicket(token, room, resourceType) {
  const segments = token.split(".");
  if (segments.length !== 3 || token.length > 4096) throw new Error("ticket_malformed");
  const header = JSON.parse(Buffer.from(segments[0], "base64url").toString("utf8"));
  if (header.alg !== "EdDSA") throw new Error("ticket_algorithm");
  if (!verifySignature(null, Buffer.from(`${segments[0]}.${segments[1]}`), publicKey, Buffer.from(segments[2], "base64url"))) throw new Error("ticket_signature");
  const claims = JSON.parse(Buffer.from(segments[1], "base64url").toString("utf8"));
  const now = Math.floor(Date.now() / 1000);
  const roles = new Set(["creator", "editor", "viewer"]);
  if (claims.iss !== issuer || claims.aud !== audience || claims.room !== room || claims.resource_type !== resourceType ||
      claims.resource_id !== claims[resourceType === "note" ? "note_id" : "drawing_id"] || !roles.has(claims.role) ||
      !Number.isInteger(claims.acl_version) || claims.acl_version < 1 || !Number.isInteger(claims.exp) || claims.exp <= now ||
      typeof claims.jti !== "string" || typeof claims.resource_id !== "string") throw new Error("ticket_claims");
  return claims;
}

function burnTicket(ticketID, expiresAt) {
  const now = Math.floor(Date.now() / 1000);
  for (const [id, expiry] of usedTicketIDs) if (expiry <= now) usedTicketIDs.delete(id);
  if (usedTicketIDs.has(ticketID)) throw new Error("ticket_replayed");
  usedTicketIDs.set(ticketID, expiresAt + 300);
}

function loadPublicKey(encoded) {
  const raw = Buffer.from(encoded, "base64");
  if (raw.length !== 32 || internalSecret.length < 32) throw new Error("collaboration service secrets are invalid");
  const prefix = Buffer.from("302a300506032b6570032100", "hex");
  return createPublicKey({ key: Buffer.concat([prefix, raw]), format: "der", type: "spki" });
}

function loadControlSecret(encoded) {
  const raw = Buffer.from(encoded, "base64");
  if (raw.length < 32) throw new Error("collaboration control secret is invalid");
  return raw;
}

async function shutdown() {
  await Promise.allSettled([...rooms.values()].map(async (pending) => persist(await pending)));
  server.close(() => process.exit(0));
  setTimeout(() => process.exit(1), 10_000).unref();
}

process.on("SIGTERM", () => void shutdown());
process.on("SIGINT", () => void shutdown());
