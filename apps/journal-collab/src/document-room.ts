import { YServer } from "y-partyserver";
import type { Connection, ConnectionContext } from "partyserver";

import { jsonResponse } from "./control-protocol";
import { documentStatusMessage } from "./document-status";
import { pushNoteProjection } from "./document-note-projection";
import { closeSocket, countSockets } from "./room-connections";
import { admitSocket } from "./room-admission";
import { exportDocumentUpdate } from "./room-document-export";
import { logRoomEvent } from "./room-log";
import { gateDocumentUpdate } from "./room-message-gate";
import { loadRoomSnapshot, saveRoomSnapshot, type RoomSnapshotContext } from "./room-snapshot";
import { bindControlResourceID, parseControlRequest } from "./room-control-request";
import { handleRoomControl, type RoomControlContext } from "./room-control";
import {
  messageIsTooLarge,
  socketBelongsToUser,
  socketIsReadOnly,
  socketIsSuperseded,
} from "./room-policy";
import { PURGED_KEY, type RoomState } from "./room-state";
import type { Env } from "./room-env";
import type { CollaborationResourceType } from "./ticket";

export type { Env } from "./room-env";

/**
 * One Durable Object per collaborative document.
 *
 * The room is the only place with a serialized view of a single document,
 * which is why two things must happen here rather than in the Worker entrypoint:
 * burning a single-use ticket id, and comparing a ticket's ACL version against
 * the newest version this room has seen.
 *
 * This class wires the Durable Object lifecycle to the modules that do the work.
 */
export abstract class PersistentDocumentRoom extends YServer<Env> {
  protected abstract readonly resourceType: CollaborationResourceType;
  protected readonly supportsMarkdownBootstrap: boolean = false;
  protected readonly supportsNoteProjection: boolean = false;
  protected readonly supportsDrawingScene: boolean = false;
  /**
   * Persist shortly after edits stop rather than on every keystroke. A single
   * burst of typing is one write instead of hundreds.
   */
  static override callbackOptions = {
    debounceWait: 2000,
    debounceMaxWait: 10000,
  };

  private readonly state: RoomState = { aclVersion: 0, purged: false };
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
    if (this.state.purged) {
      this.refuse(connection, "resource_deleted");
      return;
    }
    const url = new URL(ctx.request.url);
    const token = url.searchParams.get("ticket") ?? "";

    const admission = await admitSocket(
      {
        env: this.env,
        roomName: this.name,
        resourceType: this.resourceType,
        state: this.state,
        storage: this.ctx.storage,
        connectionCount: () => this.connectionCount(),
      },
      token,
    );
    if ("refusal" in admission) {
      this.refuse(connection, admission.refusal);
      return;
    }
    const { claims } = admission;

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
    return socketIsReadOnly(connection.state, this.state.aclVersion);
  }

  override async onMessage(
    connection: Connection,
    message: ArrayBuffer | string,
  ): Promise<void> {
    if (this.state.purged) {
      this.refuse(connection, "resource_deleted");
      return;
    }
    const size =
      typeof message === "string" ? message.length : message.byteLength;
    if (messageIsTooLarge(size)) {
      this.refuse(connection, "message_too_large");
      return;
    }
    if (typeof message !== "string") {
      const gate = gateDocumentUpdate(this.document, message);
      if (gate?.status === "blocked") {
        this.sendCustomMessage(
          connection,
          documentStatusMessage("blocked", gate.projectedBytes),
        );
        this.log("document_limit_blocked", { projected_bytes: gate.projectedBytes });
        return;
      }
      if (gate?.status === "warning") {
        this.sendCustomMessage(
          connection,
          documentStatusMessage("warning", gate.projectedBytes),
        );
      }
    }
    await super.onMessage(connection, message);
  }

  /**
   * Receives service-to-service control commands routed to this room.
   * GET is the raw document export, POST is a signed control command.
   */
  override async onRequest(request: Request): Promise<Response> {
    if (request.method === "GET") {
      return exportDocumentUpdate(request, {
        env: this.env,
        roomName: this.name,
        resourceType: this.resourceType,
        state: this.state,
        storage: this.ctx.storage,
        document: this.document,
      });
    }
    if (request.method !== "POST") {
      return jsonResponse({ code: "method_not_allowed" }, 405, {
        Allow: "GET, POST",
      });
    }
    const parsed = await parseControlRequest(request, this.env);
    if (parsed instanceof Response) return parsed;
    const binding = await bindControlResourceID(this.ctx.storage, parsed.resourceID);
    if (binding) return binding;
    return this.handleControl(parsed.command, parsed.payload);
  }

  /** Handles authenticated control commands from the Go API. */
  async handleControl(
    command: string,
    payload: Record<string, unknown>,
  ): Promise<Response> {
    return handleRoomControl(this.controlContext(), command, payload);
  }

  private controlContext(): RoomControlContext {
    return {
      document: this.document,
      storage: this.ctx.storage,
      state: this.state,
      supportsMarkdownBootstrap: this.supportsMarkdownBootstrap,
      supportsDrawingScene: this.supportsDrawingScene,
      save: () => this.onSave(),
      publishNoteProjection: () => this.publishNoteProjection(),
      disconnectSupersededSockets: () => this.disconnectSupersededSockets(),
      disconnectUsers: (userIDs) => this.disconnectUsers(userIDs),
    };
  }

  /** Closes sockets whose authorization predates the current ACL version. */
  private disconnectSupersededSockets(): void {
    for (const connection of this.getConnections()) {
      if (socketIsSuperseded(connection.state, this.state.aclVersion)) {
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
    return countSockets(this.getConnections());
  }

  private refuse(connection: Connection, code: string): void {
    closeSocket(connection, code);
    this.log("connection_refused", { code });
  }

  private close(connection: Connection, code: string): void {
    closeSocket(connection, code);
    this.log("connection_revoked", { code });
  }

  override async onStart(): Promise<void> {
    this.state.purged = (await this.ctx.storage.get<boolean>(PURGED_KEY)) === true;
    this.state.aclVersion = (await this.ctx.storage.get<number>("aclVersion")) ?? 0;
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
    if (this.state.purged) return;
    await loadRoomSnapshot(this.snapshotContext());
  }

  /**
   * Persists the whole document as a single Yjs update.
   *
   * A full state snapshot rather than an incremental log: it keeps loading to
   * one read, and Yjs updates are already compact. Storage values are capped,
   * so the snapshot is chunked.
   */
  override async onSave(): Promise<void> {
    if (this.state.purged) return;
    await saveRoomSnapshot(this.snapshotContext());
  }

  private snapshotContext(): RoomSnapshotContext {
    return {
      storage: this.ctx.storage,
      document: this.document,
      broadcast: (message) => this.broadcastCustomMessage(message),
      publishNoteProjection: this.supportsNoteProjection
        ? () => this.publishNoteProjection()
        : undefined,
      log: (event, details) => this.log(event, details),
    };
  }

  private publishNoteProjection(): Promise<void> {
    return pushNoteProjection(this.ctx.storage, this.env, this.document);
  }

  private log(event: string, details: Record<string, unknown>): void {
    logRoomEvent(
      { correlationID: this.correlationID, resourceType: this.resourceType },
      event,
      details,
    );
  }
}
