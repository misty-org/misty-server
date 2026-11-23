import { YServer } from "y-partyserver";
import type { Connection, ConnectionContext } from "partyserver";

import { jsonResponse } from "./control_protocol";
import { documentStatusMessage } from "./document_status";
import { pushNoteProjection } from "./document_note_projection";
import { closeSocket, countSockets } from "./room_connections";
import { admitSocket } from "./room_admission";
import { exportDocumentUpdate } from "./room_document_export";
import { logRoomEvent } from "./room_log";
import { gateDocumentUpdate } from "./room_message_gate";
import { awarenessClaimsSomeoneElse } from "./room_identity";
import { PendingMarkdownLease } from "./room_pending_lease";
import { loadRoomSnapshot, saveRoomSnapshot, type RoomSnapshotContext } from "./room_snapshot";
import { bindControlResourceID, parseControlRequest } from "./room_control_request";
import { handleRoomControl, type RoomControlContext } from "./room_control";
import {
  messageIsTooLarge,
  socketBelongsToUser,
  socketIsReadOnly,
  socketIsSuperseded,
} from "./room_policy";
import { PURGED_KEY, type RoomState } from "./room_state";
import type { Env } from "./room_env";
import type { CollaborationResourceType } from "./ticket";

export type { Env } from "./room_env";

export abstract class PersistentDocumentRoom extends YServer<Env> {
  protected abstract readonly resourceType: CollaborationResourceType;
  protected readonly supportsMarkdownBootstrap: boolean = false;
  protected readonly supportsNoteProjection: boolean = false;
  static override callbackOptions = {
    debounceWait: 2000,
    debounceMaxWait: 10000,
  };

  private readonly state: RoomState = { aclVersion: 0, purged: false };
  private readonly pendingMarkdown = new PendingMarkdownLease();
  private readonly correlationID = `room_${crypto.randomUUID()}`;

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
    if (
      typeof message !== "string" &&
      this.guardsIdentity() &&
      awarenessClaimsSomeoneElse(message, (connection.state as { userID?: string } | null)?.userID ?? "")
    ) {
      this.log("awareness_identity_refused", {});
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

  private keepsHistory(): boolean {
    return this.resourceType === "note";
  }

  protected guardsIdentity(): boolean {
    return this.resourceType === "app" || this.resourceType === "file";
  }

  override onCustomMessage(connection: Connection, message: string): void {
    if (!this.supportsMarkdownBootstrap || this.state.purged) return;
    const answer = this.pendingMarkdown.answer(
      this.document,
      connection.id,
      this.isReadOnly(connection),
      message,
      (id) => this.getConnection(id) !== undefined,
    );
    if (answer) this.sendCustomMessage(connection, answer);
  }

  override onClose(
    connection: Connection,
    code: number,
    reason: string,
    wasClean: boolean,
  ): void | Promise<void> {
    this.pendingMarkdown.release(connection.id);
    return super.onClose(connection, code, reason, wasClean);
  }

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
      resourceType: this.resourceType,
      keepsHistory: this.keepsHistory(),
      supportsMarkdownBootstrap: this.supportsMarkdownBootstrap,
      save: () => this.onSave(),
      publishNoteProjection: () => this.publishNoteProjection(),
      disconnectSupersededSockets: () => this.disconnectSupersededSockets(),
      disconnectUsers: (userIDs) => this.disconnectUsers(userIDs),
      closeAllSockets: (code) => this.closeAllSockets(code),
    };
  }

  private closeAllSockets(code: string): void {
    for (const connection of this.getConnections()) {
      this.close(connection, code);
    }
  }

  private disconnectSupersededSockets(): void {
    for (const connection of this.getConnections()) {
      if (socketIsSuperseded(connection.state, this.state.aclVersion)) {
        this.close(connection, "acl_superseded");
      }
    }
  }

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

  override async onLoad(): Promise<void> {
    if (this.state.purged) return;
    await loadRoomSnapshot(this.snapshotContext());
  }

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
      keepsHistory: this.keepsHistory(),
      log: (event, details) => this.log(event, details),
    };
  }

  private publishNoteProjection(): Promise<void> {
    return pushNoteProjection(this.ctx.storage, this.env, this.document);
  }

  protected log(event: string, details: Record<string, unknown>): void {
    logRoomEvent(
      { correlationID: this.correlationID, resourceType: this.resourceType },
      event,
      details,
    );
  }
}
