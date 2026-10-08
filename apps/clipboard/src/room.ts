import { DurableObject } from "cloudflare:workers";

import {
  CLIP_LIFETIME_MS,
  ClipError,
  MAX_CLIPS,
  MAX_DAILY_UPLOAD_BYTES,
  parseClip,
  quotaDay,
  type ClipRecord,
} from "./clips";

export interface Env {
  CLIPBOARD_ROOM: DurableObjectNamespace<ClipboardRoom>;
  CLIPS: R2Bucket;
  CLIPBOARD_TICKET_PUBLIC_KEY: string;
  CLIPBOARD_TICKET_PUBLIC_KEY_PREVIOUS?: string;
  CLIPBOARD_TICKET_ISSUER: string;
  CLIPBOARD_TICKET_AUDIENCE: string;
}

/** Where a room's blobs live in R2. */
export function blobKey(room: string, sha256: string): string {
  return `r/${room}/${sha256}`;
}

/**
 * One account's clipboard: the ring of recent encrypted clips, the account's
 * daily upload quota, and the WebSockets of its connected devices. Sockets use
 * hibernation, so an idle room costs nothing.
 */
export class ClipboardRoom extends DurableObject<Env> {
  private readonly sql: SqlStorage;

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    this.sql = ctx.storage.sql;
    this.sql.exec(`CREATE TABLE IF NOT EXISTS clips(
      clip_id TEXT PRIMARY KEY, revision INTEGER NOT NULL, device_id TEXT NOT NULL,
      size INTEGER NOT NULL, blobs TEXT NOT NULL, manifest TEXT NOT NULL,
      created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL)`);
    this.sql.exec(`CREATE TABLE IF NOT EXISTS usage(day TEXT PRIMARY KEY, bytes INTEGER NOT NULL)`);
  }

  override async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const room = request.headers.get("X-Clipboard-Room") ?? "";
    const device = request.headers.get("X-Clipboard-Device") ?? "";
    try {
      if (url.pathname === "/socket") return this.acceptDevice(device);
      if (url.pathname === "/reserve" && request.method === "POST") {
        const { bytes } = (await request.json()) as { bytes: number };
        return this.reserve(bytes);
      }
      if (url.pathname === "/clips" && request.method === "GET")
        return Response.json({ clips: this.clips() });
      if (url.pathname === "/clips" && request.method === "POST")
        return await this.add(room, device, await request.json());
    } catch (error) {
      if (error instanceof ClipError)
        return Response.json({ code: error.code }, { status: error.status });
      throw error;
    }
    return new Response("not found", { status: 404 });
  }

  /** Counts an upload against today's quota before the blob is stored. */
  reserve(bytes: number, now = Date.now()): Response {
    if (!Number.isSafeInteger(bytes) || bytes < 0)
      return Response.json({ code: "clip_malformed" }, { status: 400 });
    const day = quotaDay(now);
    this.sql.exec(`DELETE FROM usage WHERE day <> ?`, day);
    const used = this.sql.exec<{ bytes: number }>(`SELECT bytes FROM usage WHERE day = ?`, day).toArray()[0]?.bytes ?? 0;
    if (used + bytes > MAX_DAILY_UPLOAD_BYTES)
      return Response.json({ code: "clipboard_daily_limit" }, { status: 429 });
    this.sql.exec(
      `INSERT INTO usage(day, bytes) VALUES (?, ?) ON CONFLICT(day) DO UPDATE SET bytes = bytes + excluded.bytes`,
      day,
      bytes,
    );
    return Response.json({ used: used + bytes, limit: MAX_DAILY_UPLOAD_BYTES });
  }

  private clips(now = Date.now()): ClipRecord[] {
    return this.sql
      .exec<{
        clip_id: string;
        revision: number;
        device_id: string;
        size: number;
        blobs: string;
        manifest: string;
        created_at: number;
        expires_at: number;
      }>(`SELECT * FROM clips WHERE expires_at > ? ORDER BY created_at DESC LIMIT ?`, now, MAX_CLIPS)
      .toArray()
      .map((row) => ({ ...row, blobs: JSON.parse(row.blobs) as string[] }));
  }

  private async add(room: string, device: string, body: unknown): Promise<Response> {
    const clip = parseClip(body);
    // A clip may only point at blobs this room already holds, so a device
    // cannot announce a clip whose bytes never arrived.
    for (const sha of clip.blobs) {
      if (!(await this.env.CLIPS.head(blobKey(room, sha))))
        throw new ClipError("clip_blob_missing", 409);
    }
    const now = Date.now();
    const record: ClipRecord = {
      ...clip,
      device_id: device,
      created_at: now,
      expires_at: now + CLIP_LIFETIME_MS,
    };
    this.sql.exec(
      `INSERT OR REPLACE INTO clips(clip_id, revision, device_id, size, blobs, manifest, created_at, expires_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
      record.clip_id,
      record.revision,
      record.device_id,
      record.size,
      JSON.stringify(record.blobs),
      record.manifest,
      record.created_at,
      record.expires_at,
    );
    // Keep only the newest clips; older ciphertext expires from R2 by itself.
    this.sql.exec(
      `DELETE FROM clips WHERE clip_id NOT IN (SELECT clip_id FROM clips ORDER BY created_at DESC LIMIT ?)`,
      MAX_CLIPS,
    );
    this.broadcast({ type: "clip", clip: record });
    await this.scheduleExpiry();
    return Response.json(record, { status: 201 });
  }

  private acceptDevice(device: string): Response {
    const pair = new WebSocketPair();
    const [client, server] = [pair[0], pair[1]];
    this.ctx.acceptWebSocket(server, [device]);
    server.send(JSON.stringify({ type: "clips", clips: this.clips() }));
    return new Response(null, { status: 101, webSocket: client });
  }

  override async webSocketMessage(socket: WebSocket, message: string | ArrayBuffer): Promise<void> {
    // Devices publish over HTTP; the socket only carries keepalives.
    if (message === "ping") socket.send("pong");
  }

  override async webSocketClose(socket: WebSocket, code: number): Promise<void> {
    socket.close(code === 1005 ? 1000 : code, "closing");
  }

  override async alarm(): Promise<void> {
    this.sql.exec(`DELETE FROM clips WHERE expires_at <= ?`, Date.now());
    await this.scheduleExpiry();
  }

  private broadcast(event: unknown): void {
    const message = JSON.stringify(event);
    for (const socket of this.ctx.getWebSockets()) {
      try {
        socket.send(message);
      } catch {
        // A socket that went away is cleaned up by the runtime.
      }
    }
  }

  private async scheduleExpiry(): Promise<void> {
    const next = this.sql
      .exec<{ next: number | null }>(`SELECT MIN(expires_at) AS next FROM clips`)
      .toArray()[0]?.next;
    if (next) await this.ctx.storage.setAlarm(next);
    else await this.ctx.storage.deleteAlarm();
  }
}
