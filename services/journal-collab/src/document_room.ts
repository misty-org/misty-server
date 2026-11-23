import { YServer } from "y-partyserver";
import type { Connection, ConnectionContext } from "partyserver";

import { jsonResponse } from "./control_protocol";
import { incomingUpdate } from "./document_persistence";
import { documentStatusMessage } from "./document_status";
import { pushNoteProjection } from "./document_note_projection";
import { closeSocket, countSockets } from "./room_connections";
import { admitSocket } from "./room_admission";
import {
  awarenessClientIDs,
  awarenessOwnedBySomeoneElse,
  claimAwarenessIDs,
} from "./room_awareness";
import { exportDocumentUpdate } from "./room_document_export";
import { logRoomEvent, roomCorrelationID } from "./room_log";
import { DocumentSizeGate } from "./room_message_gate";
import { awarenessClaimsSomeoneElse } from "./room_identity";
import { PendingMarkdownLease } from "./room_pending_lease";
import { loadRoomSnapshot, saveRoomSnapshot, type RoomSnapshotContext } from "./room_snapshot";
import { bindControlResourceID, parseControlRequest } from "./room_control_request";
import { handleRoomControl, type RoomControlContext } from "./room_control";
import {
  MAX_MESSAGE_BYTES,
  messageIsTooLarge,
  socketBelongsToUser,
  socketIsReadOnly,
  socketIsSuperseded,
} from "./room_policy";
import { KEEP_DELETES_KEY, PURGED_KEY, type RoomState } from "./room_state";
import type { Env } from "./room_env";
import type { CollaborationResourceType } from "./ticket";

export type { Env } from "./room_env";

const EARLY_MESSAGE_LIMIT = 64;
const EARLY_BYTE_LIMIT = MAX_MESSAGE_BYTES;
export const SAVE_RETRY_DELAYS_MS = [2_000, 10_000, 60_000, 300_000];

interface EarlyMessages {
  messages: (ArrayBuffer | string)[];
  bytes: number;
}

interface AwarenessChange {
  added: number[];
  updated: number[];
  removed: number[];
}

function messageBytes(message: ArrayBuffer | string): number {
  return typeof message === "string" ? message.length : message.byteLength;
}

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
  private readonly sizeGate = new DocumentSizeGate(this.document);
  private readonly early = new Map<string, EarlyMessages>();
  private readonly seated = new Set<string>();
  private readonly admitted = new Set<string>();
  private readonly refusals = new Map<string, number>();
  private correlation: string | null = null;
  private changes = 0;
  private savedChanges = 0;
  private retryArmed = false;
  private retryStep = 0;
  private saveChain: Promise<void> = Promise.resolve();
  private savesStopped = false;

  override async fetch(request: Request): Promise<Response> {
    if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") return super.fetch(request);
    const url = new URL(request.url);
    url.searchParams.set("_pk", crypto.randomUUID());
    return super.fetch(new Request(url.toString(), { method: request.method, headers: request.headers }));
  }

  override *getConnections<TState = unknown>(tag?: string): Iterable<Connection<TState>> {
    for (const connection of super.getConnections<TState>(tag)) {
      if (this.admitted.has(connection.id)) yield connection;
    }
  }

  override getConnection<TState = unknown>(id: string): Connection<TState> | undefined {
    return this.admitted.has(id) ? super.getConnection<TState>(id) : undefined;
  }

  protected openSocketCount(): number {
    return countSockets(super.getConnections());
  }

  private seatCount(): number {
    let count = 0;
    for (const connection of super.getConnections()) {
      if (this.seated.has(connection.id)) count += 1;
    }
    return count;
  }

  private releaseSocket(id: string): void {
    this.early.delete(id);
    this.seated.delete(id);
    this.admitted.delete(id);
  }

  override async onConnect(
    connection: Connection,
    ctx: ConnectionContext,
  ): Promise<void> {
    const id = connection.id;
    this.early.set(id, { messages: [], bytes: 0 });
    try {
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
          connectionCount: () => this.seatCount(),
          reserve: () => this.seated.add(id),
        },
        token,
      );
      if ("refusal" in admission) {
        this.refuse(connection, admission.refusal);
        return;
      }
      if (!this.early.has(id)) {
        this.releaseSocket(id);
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
      if (claims.gc === false && this.mayKeepDeletes()) await this.keepDeletes();
      await super.onConnect(connection, ctx);

      const early = this.early.get(id);
      while (early && this.early.get(id) === early) {
        const next = early.messages.shift();
        if (next === undefined) break;
        await this.receive(connection, next);
      }
      if (!early || this.early.get(id) !== early) {
        this.releaseSocket(id);
        return;
      }
      if (this.state.purged) {
        this.refuse(connection, "resource_deleted");
        return;
      }
      if (socketIsSuperseded(connection.state, this.state.aclVersion)) {
        this.close(connection, "acl_superseded");
        return;
      }
      this.admitted.add(id);
    } finally {
      this.early.delete(id);
      if (!this.admitted.has(id)) this.seated.delete(id);
    }
  }

  private mayKeepDeletes(): boolean {
    return this.resourceType === "app" || this.resourceType === "file";
  }

  private async keepDeletes(): Promise<void> {
    if (!this.document.gc) return;
    this.document.gc = false;
    await this.ctx.storage.put(KEEP_DELETES_KEY, true);
  }

  override isReadOnly(connection: Connection): boolean {
    return socketIsReadOnly(connection.state, this.state.aclVersion);
  }

  override async onMessage(
    connection: Connection,
    message: ArrayBuffer | string,
  ): Promise<void> {
    const early = this.early.get(connection.id);
    if (early) {
      early.messages.push(message);
      early.bytes += messageBytes(message);
      if (early.messages.length > EARLY_MESSAGE_LIMIT || early.bytes > EARLY_BYTE_LIMIT) {
        this.early.delete(connection.id);
        this.refuse(connection, "message_too_large");
      }
      return;
    }
    if (!this.admitted.has(connection.id)) return;
    await this.receive(connection, message);
  }

  private async receive(
    connection: Connection,
    message: ArrayBuffer | string,
  ): Promise<void> {
    if (this.state.purged) {
      this.refuse(connection, "resource_deleted");
      return;
    }
    if (messageIsTooLarge(messageBytes(message))) {
      this.refuse(connection, "message_too_large");
      return;
    }
    if (typeof message !== "string") {
      if (
        this.guardsIdentity() &&
        awarenessClaimsSomeoneElse(message, (connection.state as { userID?: string } | null)?.userID ?? "")
      ) {
        this.log("awareness_identity_refused", {});
        return;
      }
      const awarenessIDs = awarenessClientIDs(message);
      if (awarenessIDs && awarenessOwnedBySomeoneElse(connection, awarenessIDs, this.getConnections())) {
        this.log("awareness_owner_refused", {});
        return;
      }
      if (!awarenessIDs && this.refusesUpdates() && incomingUpdate(new Uint8Array(message)) !== null) return;
      if (!awarenessIDs && !this.isReadOnly(connection)) {
        const gate = this.sizeGate.check(message);
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
    }
    await super.onMessage(connection, message);
  }

  private keepsHistory(): boolean {
    return this.resourceType === "note";
  }

  protected refusesUpdates(): boolean {
    return false;
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

  override async onClose(
    connection: Connection,
    code: number,
    reason: string,
    wasClean: boolean,
  ): Promise<void> {
    this.releaseSocket(connection.id);
    this.pendingMarkdown.release(connection.id);
    await super.onClose(connection, code, reason, wasClean);
    if (this.state.purged || this.openSocketCount() > 0 || !this.hasUnsavedWork()) return;
    try {
      await this.onSave();
    } catch {
    }
  }

  override onError(connection: Connection, error: unknown): void | Promise<void> {
    this.releaseSocket(connection.id);
    return super.onError(connection, error);
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
    for (const connection of [...super.getConnections()]) {
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

  private refuse(connection: Connection, code: string): void {
    closeSocket(connection, code);
    const count = (this.refusals.get(code) ?? 0) + 1;
    this.refusals.set(code, count);
    if ((count & (count - 1)) === 0) this.log("connection_refused", { code, count });
  }

  private close(connection: Connection, code: string): void {
    closeSocket(connection, code);
    this.log("connection_revoked", { code });
  }

  override async onStart(): Promise<void> {
    this.state.purged = (await this.ctx.storage.get<boolean>(PURGED_KEY)) === true;
    this.state.aclVersion = (await this.ctx.storage.get<number>("aclVersion")) ?? 0;
    if ((await this.ctx.storage.get<boolean>(KEEP_DELETES_KEY)) === true) this.document.gc = false;
    this.document.on("update", () => {
      this.changes += 1;
      this.retryStep = 0;
    });
    await super.onStart?.();
    this.document.awareness.on("update", (change: AwarenessChange, origin: unknown) => {
      if (!origin || typeof origin !== "object" || !("id" in origin)) return;
      claimAwarenessIDs(
        origin as Connection,
        [...change.added, ...change.updated],
        change.removed,
        this.getConnections(),
      );
    });
  }

  override async onLoad(): Promise<void> {
    if (this.state.purged) return;
    await loadRoomSnapshot(this.snapshotContext());
    this.sizeGate.measure();
    this.savedChanges = this.changes;
  }

  override onSave(): Promise<void> {
    const run = this.saveChain.then(() => this.saveOnce());
    this.saveChain = run.catch(() => undefined);
    return run;
  }

  private async saveOnce(): Promise<void> {
    if (this.state.purged || this.savesStopped) return;
    try {
      await this.persist();
      this.retryStep = 0;
    } catch (error) {
      this.scheduleSaveRetry();
      throw error;
    }
  }

  protected async stopSaves(boundMs: number): Promise<void> {
    this.savesStopped = true;
    let bound: ReturnType<typeof setTimeout> | undefined;
    await Promise.race([
      this.saveChain,
      new Promise<void>((resolve) => {
        bound = setTimeout(resolve, boundMs);
      }),
    ]);
    if (bound !== undefined) clearTimeout(bound);
  }

  protected isPurged(): boolean {
    return this.state.purged;
  }

  protected hasUnsavedWork(): boolean {
    return this.changes !== this.savedChanges;
  }

  protected async persist(): Promise<void> {
    const changes = this.changes;
    await saveRoomSnapshot(this.snapshotContext());
    this.savedChanges = changes;
  }

  protected scheduleSaveRetry(): void {
    if (this.retryArmed || this.state.purged) return;
    const wait = SAVE_RETRY_DELAYS_MS[this.retryStep];
    if (wait === undefined) {
      this.log("save_retry_gave_up", { attempts: this.retryStep });
      return;
    }
    this.retryStep += 1;
    this.retryArmed = true;
    this.ctx.storage.setAlarm(Date.now() + wait).catch((error: unknown) => {
      this.retryArmed = false;
      this.log("save_retry_schedule_failed", { error: String(error) });
    });
  }

  override async onAlarm(): Promise<void> {
    this.retryArmed = false;
    if (this.state.purged || !this.hasUnsavedWork()) return;
    try {
      await this.onSave();
    } catch (error) {
      this.log("save_retry_failed", { error: String(error), attempt: this.retryStep });
    }
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
      measured: (bytes) => this.sizeGate.measured(bytes),
      log: (event, details) => this.log(event, details),
    };
  }

  private publishNoteProjection(): Promise<void> {
    return pushNoteProjection(this.ctx.storage, this.env, this.document);
  }

  private correlationID(): string {
    if (this.correlation === null) {
      let name = "";
      try {
        name = this.name;
      } catch {
      }
      this.correlation = roomCorrelationID(this.resourceType, name);
    }
    return this.correlation;
  }

  protected log(event: string, details: Record<string, unknown>): void {
    logRoomEvent(
      { correlationID: this.correlationID(), resourceType: this.resourceType },
      event,
      details,
    );
  }
}
