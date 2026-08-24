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
  const room = { name, resourceType, resourceID, doc, awareness, clients: new Set(), aclVersion: 0, persistTimer: null, drawingRevision: 0, drawingRequests: new Map() };
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
  if (resourceType === "drawing") room.drawingRevision = readNonNegativeInteger(doc.getMap("drawing:scene").get("mistyRevision"), 0);
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

  const body = await readRequestBody(request, 512 * 1024);
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
  if (command === "drawing_scene_read") {
    if (room.resourceType !== "drawing") return writeJSON(response, 400, { code: "unknown_command" });
    return writeJSON(response, 200, drawingSceneResponse(room, payload.include_deleted === true));
  }
  if (command === "drawing_scene_apply") {
    if (room.resourceType !== "drawing") return writeJSON(response, 400, { code: "unknown_command" });
    const requestID = typeof payload.request_id === "string" ? payload.request_id.trim() : "";
    if (requestID && (requestID.length > 200 || !/^[A-Za-z0-9:._-]+$/.test(requestID))) {
      return writeJSON(response, 400, { code: "invalid_request_id" });
    }
    room.drawingRequests ||= new Map();
    if (requestID && room.drawingRequests.has(requestID)) {
      return writeJSON(response, 200, { ...room.drawingRequests.get(requestID), replayed: true });
    }
    const currentHash = drawingSceneHash(room.doc);
    if (typeof payload.base_hash === "string" && payload.base_hash !== currentHash) {
      return writeJSON(response, 409, { code: "drawing_conflict", content_hash: currentHash });
    }
    let mutation;
    try {
      mutation = buildDrawingSceneMutation(room.doc, payload);
    } catch (error) {
      return writeJSON(response, 400, { code: error instanceof Error ? error.message : "invalid_drawing_scene" });
    }
    const projected = new Y.Doc();
    Y.applyUpdate(projected, Y.encodeStateAsUpdate(room.doc));
    applyDrawingSceneMutation(projected, mutation);
    const projectedBytes = Y.encodeStateAsUpdate(projected).byteLength;
    projected.destroy();
    if (projectedBytes > maxDocumentBytes) return writeJSON(response, 413, { code: "document_too_large" });
    room.drawingRevision = Number(room.drawingRevision || 0) + 1;
    mutation.scene.mistyRevision = room.drawingRevision;
    applyDrawingSceneMutation(room.doc, mutation);
    await persist(room);
    const result = {
      ok: true,
      revision: room.drawingRevision,
      content_hash: drawingSceneHash(room.doc),
      changed: mutation.changed,
      deleted: mutation.deleted,
      element_count: drawingElements(room.doc, false).length,
    };
    if (requestID) {
      room.drawingRequests.set(requestID, result);
      if (room.drawingRequests.size > 500) room.drawingRequests.delete(room.drawingRequests.keys().next().value);
    }
    return writeJSON(response, 200, result);
  }
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

const drawingElementTypes = new Set(["rectangle", "diamond", "ellipse", "text", "line", "arrow", "freedraw", "image", "frame", "magicframe", "iframe", "embeddable"]);

function drawingNumber(value, fallback, minimum = -1_000_000, maximum = 1_000_000) {
  return typeof value === "number" && Number.isFinite(value) ? Math.max(minimum, Math.min(maximum, value)) : fallback;
}

function drawingInteger(value, fallback) {
  return Number.isInteger(value) && value > 0 ? Math.min(value, 2_147_483_646) : fallback;
}

function drawingRandomInteger() {
  return 1 + Math.floor(Math.random() * 2_147_483_645);
}

function drawingPoints(value, fallback) {
  if (!Array.isArray(value) || value.length < 2 || value.length > 10_000) return fallback;
  const normalized = [];
  for (const point of value) {
    if (!Array.isArray(point) || point.length !== 2) return fallback;
    normalized.push([drawingNumber(point[0], 0), drawingNumber(point[1], 0)]);
  }
  return normalized;
}

function normalizeDrawingElement(input, current, now) {
  const merged = { ...(current || {}), ...input };
  const id = typeof merged.id === "string" ? merged.id.trim() : "";
  const type = typeof merged.type === "string" ? merged.type : "";
  if (!id || id.length > 128 || !/^[A-Za-z0-9_-]+$/.test(id) || !drawingElementTypes.has(type)) throw new Error("invalid_drawing_element");
  let fallbackWidth = 100, fallbackHeight = 100, normalizedPoints = null;
  if (type === "text") {
    const text = typeof merged.text === "string" ? merged.text : "", fontSize = drawingNumber(merged.fontSize, 20, 1, 512), lines = text.split("\n");
    fallbackWidth = Math.max(10, Math.min(1_000_000, Math.max(...lines.map((line) => line.length), 1) * fontSize * 0.62));
    fallbackHeight = Math.max(fontSize, lines.length * fontSize * 1.25);
  } else if (type === "line" || type === "arrow" || type === "freedraw") {
    normalizedPoints = drawingPoints(merged.points, [[0, 0], [100, 0]]);
    const xs = normalizedPoints.map((point) => point[0]), ys = normalizedPoints.map((point) => point[1]);
    fallbackWidth = Math.max(...xs) - Math.min(...xs); fallbackHeight = Math.max(...ys) - Math.min(...ys);
  }
  const width = drawingNumber(merged.width, fallbackWidth, 0);
  const height = drawingNumber(merged.height, fallbackHeight, 0);
  const element = {
    ...merged, id, type,
    x: drawingNumber(merged.x, 0), y: drawingNumber(merged.y, 0),
    strokeColor: typeof merged.strokeColor === "string" ? merged.strokeColor : "#1e1e1e",
    backgroundColor: typeof merged.backgroundColor === "string" ? merged.backgroundColor : "transparent",
    fillStyle: ["hachure", "cross-hatch", "solid", "zigzag"].includes(merged.fillStyle) ? merged.fillStyle : "solid",
    strokeWidth: drawingNumber(merged.strokeWidth, 2, 0, 20),
    strokeStyle: ["solid", "dashed", "dotted"].includes(merged.strokeStyle) ? merged.strokeStyle : "solid",
    roundness: merged.roundness === null || (merged.roundness && typeof merged.roundness === "object" && !Array.isArray(merged.roundness)) ? merged.roundness : null,
    roughness: drawingNumber(merged.roughness, 1, 0, 3), opacity: drawingNumber(merged.opacity, 100, 0, 100),
    width, height, angle: drawingNumber(merged.angle, 0, -Math.PI * 2, Math.PI * 2),
    seed: drawingInteger(merged.seed, drawingRandomInteger()),
    version: drawingInteger(current?.version, 0) + 1, versionNonce: drawingRandomInteger(),
    index: typeof merged.index === "string" ? merged.index : null,
    isDeleted: merged.isDeleted === true,
    groupIds: Array.isArray(merged.groupIds) ? merged.groupIds.filter((value) => typeof value === "string").slice(0, 100) : [],
    frameId: typeof merged.frameId === "string" ? merged.frameId : null,
    boundElements: Array.isArray(merged.boundElements) ? merged.boundElements.slice(0, 100) : null,
    updated: now, link: typeof merged.link === "string" ? merged.link.slice(0, 4096) : null, locked: merged.locked === true,
  };
  if (type === "text") {
    const text = typeof merged.text === "string" ? merged.text.slice(0, 100_000) : "";
    element.text = text; element.originalText = typeof merged.originalText === "string" ? merged.originalText.slice(0, 100_000) : text;
    element.fontSize = drawingNumber(merged.fontSize, 20, 1, 512); element.fontFamily = drawingInteger(merged.fontFamily, 5);
    element.textAlign = ["left", "center", "right"].includes(merged.textAlign) ? merged.textAlign : "left";
    element.verticalAlign = ["top", "middle", "bottom"].includes(merged.verticalAlign) ? merged.verticalAlign : "top";
    element.containerId = typeof merged.containerId === "string" ? merged.containerId : null;
    element.autoResize = merged.autoResize !== false; element.lineHeight = drawingNumber(merged.lineHeight, 1.25, 0.5, 4);
  } else if (type === "line" || type === "arrow") {
    element.points = normalizedPoints || [[0, 0], [width || 100, height]]; element.lastCommittedPoint = null;
    element.startBinding = merged.startBinding && typeof merged.startBinding === "object" ? merged.startBinding : null;
    element.endBinding = merged.endBinding && typeof merged.endBinding === "object" ? merged.endBinding : null;
    element.startArrowhead = typeof merged.startArrowhead === "string" ? merged.startArrowhead : null;
    element.endArrowhead = typeof merged.endArrowhead === "string" ? merged.endArrowhead : type === "arrow" ? "arrow" : null;
    if (type === "arrow") element.elbowed = merged.elbowed === true;
  } else if (type === "freedraw") {
    normalizedPoints ||= [[0, 0], [width || 1, height || 1]];
    element.points = normalizedPoints;
    element.pressures = Array.isArray(merged.pressures) ? merged.pressures.slice(0, element.points.length).map((value) => drawingNumber(value, 0.5, 0, 1)) : [];
    element.simulatePressure = merged.simulatePressure !== false; element.lastCommittedPoint = null;
  } else if (type === "image") {
    element.fileId = typeof merged.fileId === "string" ? merged.fileId : null;
    element.status = ["pending", "saved", "error"].includes(merged.status) ? merged.status : element.fileId ? "saved" : "pending";
    element.scale = Array.isArray(merged.scale) && merged.scale.length === 2 ? merged.scale : [1, 1];
    element.crop = merged.crop && typeof merged.crop === "object" ? merged.crop : null;
  } else if (type === "frame" || type === "magicframe") element.name = typeof merged.name === "string" ? merged.name.slice(0, 500) : null;
  return element;
}

function drawingElements(doc, includeDeleted = true) {
  return [...doc.getMap("drawing:elements").values()]
    .filter((element) => includeDeleted || element.isDeleted !== true)
    .sort((left, right) => String(left.index || "").localeCompare(String(right.index || "")) || String(left.id).localeCompare(String(right.id)));
}

function drawingSceneHash(doc) {
  return createHash("sha256").update(JSON.stringify({ elements: drawingElements(doc, true), scene: Object.fromEntries(doc.getMap("drawing:scene")), files: Object.fromEntries(doc.getMap("drawing:files")) })).digest("hex");
}

function drawingSceneResponse(room, includeDeleted) {
  return { ok: true, revision: Number(room.drawingRevision || 0), content_hash: drawingSceneHash(room.doc), elements: drawingElements(room.doc, includeDeleted), scene: Object.fromEntries(room.doc.getMap("drawing:scene")), files: Object.fromEntries(room.doc.getMap("drawing:files")) };
}

function buildDrawingSceneMutation(doc, payload) {
  const rawElements = payload.elements || [], rawDeletes = payload.delete_element_ids || [];
  if (!Array.isArray(rawElements) || rawElements.length > 500 || !Array.isArray(rawDeletes) || rawDeletes.length > 500) throw new Error("invalid_drawing_scene");
  const next = new Map(doc.getMap("drawing:elements"));
  let changed = 0, deleted = 0;
  for (const raw of rawElements) {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new Error("invalid_drawing_element");
    const id = typeof raw.id === "string" ? raw.id.trim() : "";
    next.set(id, normalizeDrawingElement(raw, next.get(id), Date.now())); changed += 1;
  }
  const deleteIDs = new Set(rawDeletes.filter((value) => typeof value === "string"));
  if (payload.mode === "replace") {
    const retained = new Set(rawElements.map((value) => typeof value?.id === "string" ? value.id.trim() : ""));
    for (const [id, element] of next) if (!retained.has(id) && element.isDeleted !== true) deleteIDs.add(id);
  } else if (payload.mode !== undefined && payload.mode !== "merge") throw new Error("invalid_drawing_scene");
  for (const id of deleteIDs) {
    const element = next.get(id); if (!element || element.isDeleted === true) continue;
    next.set(id, normalizeDrawingElement({ id, isDeleted: true }, element, Date.now())); deleted += 1;
  }
  const scene = {};
  if (payload.scene !== undefined) {
    if (!payload.scene || typeof payload.scene !== "object" || Array.isArray(payload.scene)) throw new Error("invalid_drawing_scene");
    if (typeof payload.scene.viewBackgroundColor === "string" && payload.scene.viewBackgroundColor.length <= 100) scene.viewBackgroundColor = payload.scene.viewBackgroundColor;
  }
  return { elements: [...next.values()], scene, changed, deleted };
}

function applyDrawingSceneMutation(doc, mutation) {
  doc.transact(() => {
    const elements = doc.getMap("drawing:elements");
    for (const element of mutation.elements) elements.set(String(element.id), element);
    const scene = doc.getMap("drawing:scene");
    for (const [key, value] of Object.entries(mutation.scene)) scene.set(key, value);
  }, "misty:drawing-control");
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
