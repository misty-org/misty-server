import { YServer } from "y-partyserver";
import * as Y from "yjs";
import type { Connection, ConnectionContext } from "partyserver";

import {
  TicketError,
  verifyTicketWithRotation,
  type CollaborationResourceType,
} from "./ticket";
import {
  isRecord,
  jsonResponse,
  signServiceRequest,
  verifyControlRequestWithRotation,
} from "./control-protocol";
import {
  claimTicketID,
  messageIsTooLarge,
  roomIsFull,
  socketBelongsToUser,
  socketIsReadOnly,
  socketIsSuperseded,
} from "./room-policy";
import {
  DOCUMENT_WARNING_BYTES,
  MAX_DOCUMENT_BYTES,
  projectedDocumentBytes,
  readDocumentSnapshot,
  writeDocumentSnapshot,
} from "./document-persistence";

/**
 * One Durable Object per collaborative document.
 *
 * The room is the only place with a serialized view of a single document,
 * which is why two things must happen here rather than in the Worker entrypoint:
 * burning a single-use ticket id, and comparing a ticket's ACL version against
 * the newest version this room has seen.
 */

export interface Env {
  NOTE_ROOM: DurableObjectNamespace;
  DRAWING_ROOM: DurableObjectNamespace;
  JOURNAL_COLLAB_TICKET_PUBLIC_KEY: string;
  JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS?: string;
  JOURNAL_COLLAB_CONTROL_SECRET: string;
  JOURNAL_COLLAB_CONTROL_SECRET_PREVIOUS?: string;
  JOURNAL_COLLAB_PROJECTION_SECRET: string;
  JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS?: string;
  JOURNAL_COLLAB_ISSUER: string;
  JOURNAL_COLLAB_AUDIENCE: string;
  MISTY_INTERNAL_API_BASE: string;
}

/** Used ticket ids are kept a little past ticket expiry, then swept. */
const JTI_RETENTION_MS = 5 * 60 * 1000;
/** Control requests are tiny JSON envelopes, never document snapshots. */
const MAX_CONTROL_BODY_BYTES = 128 * 1024;
const BOOTSTRAP_APPLIED_KEY = "bootstrap:applied";

function replaceSharedText(text: Y.Text, value: string): void {
  text.delete(0, text.length);
  if (value) text.insert(0, value);
}

function markdownToPlainText(markdown: string): string {
  return markdown
    .replace(/!\[[^\]]*\]\([^)]*\)/gu, " ")
    .replace(/\[([^\]]+)\]\([^)]*\)/gu, "$1")
    .replace(/[`*_>#~-]+/gu, " ")
    .replace(/\s+/gu, " ")
    .trim();
}

export abstract class PersistentDocumentRoom extends YServer<Env> {
  protected abstract readonly resourceType: CollaborationResourceType;
  protected readonly supportsMarkdownBootstrap: boolean = false;
  protected readonly supportsNoteProjection: boolean = false;
  /**
   * Persist shortly after edits stop rather than on every keystroke. A single
   * burst of typing is one write instead of hundreds.
   */
  static override callbackOptions = {
    debounceWait: 2000,
    debounceMaxWait: 10000,
  };

  /** Newest ACL version this room has been told about. */
  private aclVersion = 0;
  /** Per-object-instance id for joining privacy-safe Worker log events. */
  private readonly correlationID = `room_${crypto.randomUUID()}`;

  /**
   * Authorizes the socket before it joins.
   *
   * Everything here fails closed: any thrown error or non-101 response means
   * the connection is refused, so a bug cannot accidentally admit a client.
   */
  override async onConnect(
    connection: Connection,
    ctx: ConnectionContext,
  ): Promise<void> {
    const url = new URL(ctx.request.url);
    const token = url.searchParams.get("ticket") ?? "";

    let claims;
    try {
      claims = await verifyTicketWithRotation(
        token,
        {
          publicKeyBase64: this.env.JOURNAL_COLLAB_TICKET_PUBLIC_KEY,
          issuer: this.env.JOURNAL_COLLAB_ISSUER,
          audience: this.env.JOURNAL_COLLAB_AUDIENCE,
          room: this.name,
          resourceType: this.resourceType,
        },
        this.env.JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS,
      );
    } catch (error) {
      this.refuse(
        connection,
        error instanceof TicketError ? error.code : "ticket_invalid",
      );
      return;
    }

    // A ticket older than the room's known ACL version is stale by definition:
    // permissions changed after it was minted, so the client must ask the API
    // for a fresh one that reflects the new grant set.
    if (claims.acl_version < this.aclVersion) {
      this.refuse(connection, "ticket_acl_stale");
      return;
    }
    if (claims.acl_version > this.aclVersion) {
      this.aclVersion = claims.acl_version;
      await this.ctx.storage.put("aclVersion", this.aclVersion);
    }

    // Single use. The check and the write are one storage transaction so two
    // simultaneous connections cannot both claim the same ticket.
    if (!(await this.burnTicketID(claims.jti))) {
      this.refuse(connection, "ticket_replayed");
      return;
    }

    if (roomIsFull(this.connectionCount())) {
      this.refuse(connection, "room_full");
      return;
    }

    connection.setState({
      userID: claims.sub,
      role: claims.role,
      aclVersion: claims.acl_version,
      resourceID: claims.resource_id,
      spaceID: claims.space_id,
    });
    await Promise.all([
      this.ctx.storage.put("resourceID", claims.resource_id),
      this.ctx.storage.put("spaceID", claims.space_id),
    ]);
    await super.onConnect(connection, ctx);
  }

  /**
   * Viewers receive document state and awareness but may not submit updates.
   * y-partyserver consults this before applying anything to the document.
   */
  override isReadOnly(connection: Connection): boolean {
    return socketIsReadOnly(connection.state, this.aclVersion);
  }

  override async onMessage(
    connection: Connection,
    message: ArrayBuffer | string,
  ): Promise<void> {
    const size =
      typeof message === "string" ? message.length : message.byteLength;
    if (messageIsTooLarge(size)) {
      this.refuse(connection, "message_too_large");
      return;
    }
    if (typeof message !== "string") {
      const bytes = message instanceof ArrayBuffer ? new Uint8Array(message) : new Uint8Array(message);
      const projectedBytes = projectedDocumentBytes(this.document, bytes);
      if (projectedBytes !== null && projectedBytes > MAX_DOCUMENT_BYTES) {
        this.sendDocumentStatus(connection, "blocked", projectedBytes);
        this.log("document_limit_blocked", { projected_bytes: projectedBytes });
        return;
      }
      if (projectedBytes !== null && projectedBytes >= DOCUMENT_WARNING_BYTES) {
        this.sendDocumentStatus(connection, "warning", projectedBytes);
      }
    }
    await super.onMessage(connection, message);
  }

  /**
   * Receives service-to-service control commands routed to this room.
   *
   * The timestamp is part of the signature and has a short acceptance window,
   * so a captured request cannot be replayed later. The command body is signed
   * byte-for-byte before JSON parsing.
   */
  override async onRequest(request: Request): Promise<Response> {
    if (request.method === "GET") {
      return this.handleDocumentExport(request);
    }
    if (request.method !== "POST") {
      return jsonResponse({ code: "method_not_allowed" }, 405, {
        Allow: "GET, POST",
      });
    }
    const body = new Uint8Array(await request.arrayBuffer());
    if (body.byteLength > MAX_CONTROL_BODY_BYTES) {
      return jsonResponse({ code: "body_too_large" }, 413);
    }
    const timestamp = request.headers.get("X-Misty-Timestamp") ?? "";
    const signature = request.headers.get("X-Misty-Signature") ?? "";
    if (
      !(await verifyControlRequestWithRotation(
        this.env.JOURNAL_COLLAB_CONTROL_SECRET,
        this.env.JOURNAL_COLLAB_CONTROL_SECRET_PREVIOUS,
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
    return this.handleControl(envelope.command, envelope.payload);
  }

  /**
   * Returns a portable raw Yjs update directly to an authenticated client.
   * The Go API mints the ordinary short-lived, single-use viewer ticket only
   * after rechecking access; image bodies remain in R2 and are never embedded.
   */
  private async handleDocumentExport(request: Request): Promise<Response> {
    const url = new URL(request.url);
    if (url.searchParams.get("export") !== "1") {
      return jsonResponse({ code: "not_found" }, 404);
    }
    let claims;
    try {
      claims = await verifyTicketWithRotation(
        url.searchParams.get("ticket") ?? "",
        {
          publicKeyBase64: this.env.JOURNAL_COLLAB_TICKET_PUBLIC_KEY,
          issuer: this.env.JOURNAL_COLLAB_ISSUER,
          audience: this.env.JOURNAL_COLLAB_AUDIENCE,
          room: this.name,
          resourceType: this.resourceType,
        },
        this.env.JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS,
      );
    } catch (error) {
      return jsonResponse(
        {
          code:
            error instanceof TicketError ? error.code : "ticket_invalid",
        },
        401,
      );
    }
    if (claims.acl_version < this.aclVersion) {
      return jsonResponse({ code: "ticket_acl_stale" }, 401);
    }
    if (!(await this.burnTicketID(claims.jti))) {
      return jsonResponse({ code: "ticket_replayed" }, 401);
    }
    const update = Y.encodeStateAsUpdate(this.document);
    if (update.byteLength > MAX_DOCUMENT_BYTES) {
      return jsonResponse({ code: "document_too_large" }, 413);
    }
    return new Response(update, {
      status: 200,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Cache-Control": "private, no-store",
        "Content-Type": "application/vnd.yjs.update",
        "Content-Length": String(update.byteLength),
        "X-Content-Type-Options": "nosniff",
      },
    });
  }

  /** Handles authenticated control commands from the Go API. */
  async handleControl(
    command: string,
    payload: Record<string, unknown>,
  ): Promise<Response> {
    switch (command) {
      case "bootstrap": {
        if (!this.supportsMarkdownBootstrap) {
          return jsonResponse({ code: "unknown_command" }, 400);
        }
        const title =
          typeof payload.title === "string" ? payload.title.trim() : "";
        const markdown =
          typeof payload.markdown === "string" ? payload.markdown.trim() : "";
        if (
          !title ||
          title.length > 500 ||
          !markdown ||
          markdown.length > 100_000
        ) {
          return jsonResponse({ code: "invalid_bootstrap" }, 400);
        }
        if (
          (await this.ctx.storage.get<boolean>(BOOTSTRAP_APPLIED_KEY)) === true
        ) {
          return jsonResponse({ ok: true, initialized: false });
        }

        // A user may open and begin editing before this retryable command
        // arrives. Never replace a document that already has shared state.
        const initialized = this.document.share.size === 0;
        if (initialized) {
          this.document.transact(() => {
            const metadata = this.document.getMap<unknown>("misty:document");
            metadata.set("schema", "tiptap-v1");
            metadata.set("pending_version", 1);
            metadata.set("pending_markdown", markdown);
            replaceSharedText(this.document.getText("misty:title"), title);
            replaceSharedText(this.document.getText("misty:markdown"), markdown);
          });
          await this.onSave();
        }
        // Written after the document snapshot: a crash can cause one harmless
        // retry, but can never claim success before the content is durable.
        await this.ctx.storage.put(BOOTSTRAP_APPLIED_KEY, true);
        return jsonResponse({ ok: true, initialized });
      }
      case "replace_markdown": {
        if (!this.supportsMarkdownBootstrap) {
          return jsonResponse({ code: "unknown_command" }, 400);
        }
        const title = typeof payload.title === "string" ? payload.title.trim() : "";
        const markdown = typeof payload.markdown === "string" ? payload.markdown.trim() : "";
        if (!title || title.length > 500 || markdown.length > 100_000) {
          return jsonResponse({ code: "invalid_note_content" }, 400);
        }
        const revision = ((await this.ctx.storage.get<number>("pendingVersion")) ?? 0) + 1;
        this.document.transact(() => {
          const metadata = this.document.getMap<unknown>("misty:document");
          metadata.set("schema", "tiptap-v1");
          metadata.set("pending_version", revision);
          metadata.set("pending_markdown", markdown);
          replaceSharedText(this.document.getText("misty:title"), title);
          replaceSharedText(this.document.getText("misty:markdown"), markdown);
        });
        await this.ctx.storage.put("pendingVersion", revision);
        await this.onSave();
        return jsonResponse({ ok: true });
      }
      case "acl": {
        const version = Number(payload.acl_version);
        if (!Number.isInteger(version) || version < 1) {
          return jsonResponse({ code: "invalid_acl_version" }, 400);
        }
        // Only ever moves forward, so an out-of-order retry of an older
        // command cannot re-admit users who were already revoked.
        if (version > this.aclVersion) {
          this.aclVersion = version;
          await this.ctx.storage.put("aclVersion", version);
        }
        this.disconnectSupersededSockets();
        return jsonResponse({ ok: true, acl_version: this.aclVersion });
      }
      case "disconnect": {
        const userIDs = Array.isArray(payload.user_ids)
          ? (payload.user_ids as string[])
          : null;
        this.disconnectUsers(userIDs);
        return jsonResponse({ ok: true });
      }
      case "status": {
        const persisted = await readDocumentSnapshot(this.ctx.storage);
        return jsonResponse({
          ok: true,
          acl_version: this.aclVersion,
          document_bytes: Y.encodeStateAsUpdate(this.document).byteLength,
          persisted_bytes: persisted?.update.byteLength ?? 0,
          persistence_source: persisted?.source ?? "empty",
        });
      }
      case "purge": {
        // Idempotent: purging an already-empty room succeeds, which is what
        // lets the Go retention worker retry safely.
        this.disconnectUsers(null);
        await this.ctx.storage.deleteAll();
        this.aclVersion = 0;
        // The active Y.Doc still contains the deleted content. Abort this
        // object after the response so no later request or debounced save can
        // serve or recreate it; the next request starts with empty storage.
        setTimeout(() => this.ctx.abort("room_purged"), 25);
        return jsonResponse({ ok: true, purged: true });
      }
      default:
        return jsonResponse({ code: "unknown_command" }, 400);
    }
  }

  /** Closes sockets whose authorization predates the current ACL version. */
  private disconnectSupersededSockets(): void {
    for (const connection of this.getConnections()) {
      if (socketIsSuperseded(connection.state, this.aclVersion)) {
        this.close(connection, "acl_superseded");
      }
    }
  }

  /** Closes every socket for the listed users, or all sockets when null. */
  private disconnectUsers(userIDs: string[] | null): void {
    const targeted = userIDs === null ? null : new Set(userIDs);
    for (const connection of this.getConnections()) {
      if (socketBelongsToUser(connection.state, targeted)) {
        this.close(connection, "access_revoked");
      }
    }
  }

  private connectionCount(): number {
    let count = 0;
    for (const _ of this.getConnections()) count += 1;
    return count;
  }

  private refuse(connection: Connection, code: string): void {
    // 1008 is "policy violation". The code alone is sent; nothing that
    // identifies the document or its members goes over a refused socket.
    try {
      connection.close(1008, code);
    } catch {
      /* already closing */
    }
    this.log("connection_refused", { code });
  }

  private close(connection: Connection, code: string): void {
    try {
      connection.close(1008, code);
    } catch {
      /* already closing */
    }
    this.log("connection_revoked", { code });
  }

  /**
   * Records a ticket id if it has not been seen. Returns false on replay.
   *
   * Durable Object storage is serialized per object, so this read-then-write
   * cannot interleave with another connection to the same room.
   */
  private async burnTicketID(jti: string): Promise<boolean> {
    const now = Date.now();
    if (!(await claimTicketID(this.ctx.storage, jti, now))) return false;
    // Opportunistic sweep so used ids do not accumulate forever.
    if (Math.random() < 0.05) await this.sweepTicketIDs(now);
    return true;
  }

  private async sweepTicketIDs(now: number): Promise<void> {
    const entries = await this.ctx.storage.list<number>({
      prefix: "jti:",
      limit: 200,
    });
    const stale: string[] = [];
    for (const [key, storedAt] of entries) {
      if (now - storedAt > JTI_RETENTION_MS) stale.push(key);
    }
    if (stale.length > 0) await this.ctx.storage.delete(stale);
  }

  override async onStart(): Promise<void> {
    this.aclVersion = (await this.ctx.storage.get<number>("aclVersion")) ?? 0;
    await super.onStart?.();
  }

  /**
   * Restores the document from Durable Object storage.
   *
   * y-partyserver's base implementations of onLoad/onSave are no-ops, so
   * without these the document would live only in memory and be lost the
   * moment the object is evicted.
   */
  override async onLoad(): Promise<void> {
    const snapshot = await readDocumentSnapshot(this.ctx.storage);
    if (snapshot) {
      Y.applyUpdate(this.document, snapshot.update);
      if (snapshot.source !== "current") {
        this.log("document_snapshot_recovered", { source: snapshot.source });
      }
    }
  }

  /**
   * Persists the whole document as a single Yjs update.
   *
   * A full state snapshot rather than an incremental log: it keeps loading to
   * one read, and Yjs updates are already compact. Storage values are capped,
   * so the snapshot is chunked.
   */
  override async onSave(): Promise<void> {
    const update = Y.encodeStateAsUpdate(this.document);
    try {
      const manifest = await writeDocumentSnapshot(this.ctx.storage, update);
      this.broadcastDocumentStatus(
        manifest.byteLength >= DOCUMENT_WARNING_BYTES ? "warning" : "saved",
        manifest.byteLength,
      );
      if (this.supportsNoteProjection) await this.publishNoteProjection();
    } catch (error) {
      const code =
        error instanceof Error && error.message === "document_limit_exceeded"
          ? "document_limit_exceeded"
          : "document_persistence_failed";
      this.broadcastDocumentStatus("error", update.byteLength, code);
      this.log(code, { document_bytes: update.byteLength });
      throw error;
    }
  }

  private async publishNoteProjection(): Promise<void> {
    const noteID = (await this.ctx.storage.get<string>("resourceID")) ?? "";
    if (!noteID) return;
    const title = this.document.getText("misty:title").toString().trim() || "Untitled note";
    const markdown = this.document.getText("misty:markdown").toString();
    const metadata = this.document.getMap<unknown>("misty:document");
    const outgoing = Array.isArray(metadata.get("outgoing_note_ids"))
      ? (metadata.get("outgoing_note_ids") as unknown[]).filter((value): value is string => typeof value === "string")
      : [];
    const revision = ((await this.ctx.storage.get<number>("projectionRevision")) ?? 0) + 1;
    const payload = JSON.stringify({
      note_id: noteID,
      revision,
      title,
      markdown,
      plain_text: markdownToPlainText(markdown),
      outgoing_note_ids: outgoing,
    });
    const bytes = new TextEncoder().encode(payload);
    const timestamp = String(Math.floor(Date.now() / 1000));
    const signature = await signServiceRequest(this.env.JOURNAL_COLLAB_PROJECTION_SECRET, timestamp, bytes);
    const response = await fetch(`${this.env.MISTY_INTERNAL_API_BASE.replace(/\/$/u, "")}/internal/journal/note-projections`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Misty-Timestamp": timestamp, "X-Misty-Signature": signature },
      body: bytes,
    });
    if (!response.ok) throw new Error(`projection_callback_${response.status}`);
    await this.ctx.storage.put("projectionRevision", revision);
  }

  private sendDocumentStatus(
    connection: Connection,
    status: "warning" | "blocked",
    documentBytes: number,
    code?: string,
  ): void {
    this.sendCustomMessage(
      connection,
      JSON.stringify({
        type: "document_status",
        status,
        code,
        document_bytes: documentBytes,
        maximum_bytes: MAX_DOCUMENT_BYTES,
      }),
    );
  }

  private broadcastDocumentStatus(
    status: "saved" | "warning" | "error",
    documentBytes: number,
    code?: string,
  ): void {
    this.broadcastCustomMessage(
      JSON.stringify({
        type: "document_status",
        status,
        code,
        document_bytes: documentBytes,
        maximum_bytes: MAX_DOCUMENT_BYTES,
      }),
    );
  }

  private log(event: string, details: Record<string, unknown>): void {
    console.error(
      JSON.stringify({
        level: event.endsWith("failed") || event.endsWith("exceeded") ? "error" : "warn",
        event,
        correlation_id: this.correlationID,
        resource_type: this.resourceType,
        ...details,
      }),
    );
  }
}
