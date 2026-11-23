import * as encoding from "lib0/encoding";
import type { Connection, ConnectionContext } from "partyserver";
import * as Y from "yjs";

import { PersistentDocumentRoom } from "./document_room";
import { postRoomCallbackAnswer } from "./room_callback";
import { closeSocket } from "./room_connections";
import { baseClaimantHolds, readDriveVersions, versionClaimHolds, type VersionClaim } from "./room_file_versions";
import { KEEP_DELETES_KEY } from "./room_state";

const LEGACY_SAVED_KEY = "file:saved_vector";
const SAVED_KEY = "file:saved_fingerprint";
const BASE_KEY = "file:base_version";
const RENAMED_KEY = "file:renamed_version";
const GENERATION_KEY = "file:generation";
const OUTSIDE_KEY = "file:outside_generation";
const OUTSIDE_VERSION_KEY = "file:outside_version";
const CONTENT_MODE_KEY = "file:content_generation";
const FILE_META = "misty:file";
const FILE_TEXT = "text";
const DELETE_BATCH = 128;
const ANNOUNCE_AFTER_MS = 250;
const MAX_FINGERPRINT_LENGTH = 128;
const MAX_LEGACY_VECTOR_LENGTH = 64 * 1024;
const RELOAD_ORIGIN = "misty:file-reload";
const WRITER_QUIET_MS = 30_000;
const WRITER_ANSWER_MS = 10_000;
const MAX_NONCE_LENGTH = 64;
const RELOAD_SAVE_WAIT_MS = 20_000;

const RELOAD_ABORT_DELAY_MS = 250;
const EDITORS_KEY = "file:editors";
const MAX_EDITORS = 5;
const RESOLVE_BOUND_MS = 60_000;
const RESOLVE_SAVE_WAIT_MS = 5_000;
const RESOLVE_COPY_MS = 15_000;
const RESOLVE_STOP_WAIT_MS = 8_000;
const MAX_RESOLVE_TRIES = 2;
const MAX_NAME_LENGTH = 255;
const FILE_TEXTS_PATH = "/internal/journal/file-texts";
const MAX_CLAIMS = 8;
const MESSAGE_SYNC = 0;
const SYNC_STEP_1 = 0;

interface TextTarget {
  spaceID: string;
  itemID: string;
  userID: string;
  base: number;
}

function bounded(promise: Promise<unknown>, boundMs: number): Promise<void> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  return Promise.race([
    promise.then(
      () => undefined,
      () => undefined,
    ),
    new Promise<void>((resolve) => {
      timer = setTimeout(resolve, boundMs);
    }),
  ]).finally(() => {
    if (timer !== undefined) clearTimeout(timer);
  });
}

function nameOf(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 && value.length <= MAX_NAME_LENGTH ? value : null;
}

function versionOf(value: unknown): number | null {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : null;
}

function clearDocument(document: Y.Doc): void {
  document.transact((transaction) => {
    for (const type of document.share.values()) {
      for (const item of type._map.values()) if (!item.deleted) item.delete(transaction);
      for (let item = type._start; item !== null; item = item.right) if (!item.deleted) item.delete(transaction);
    }
  }, RELOAD_ORIGIN);
}

interface FileSocketState {
  userID?: string;
  resourceID?: string;
}

function base64(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function sameBytes(left: Uint8Array, right: Uint8Array): boolean {
  return left.length === right.length && left.every((byte, index) => byte === right[index]);
}

function snapshotBytes(document: Y.Doc): Uint8Array {
  return Y.encodeSnapshot(Y.snapshot(document));
}

async function fingerprintOf(bytes: Uint8Array): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new Uint8Array(bytes).buffer);
  return base64(new Uint8Array(digest));
}

function validFingerprint(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 && value.length <= MAX_FINGERPRINT_LENGTH ? value : null;
}

function legacyFingerprint(document: Y.Doc, vector: string): Promise<string> | null {
  if (vector !== base64(Y.encodeStateVector(document))) return null;
  const snapshot = Y.snapshot(document);
  if (snapshot.ds.clients.size > 0) return null;
  return fingerprintOf(Y.encodeSnapshot(snapshot));
}

export class FileRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "file" as const;
  private writer: string | null = null;
  private savedFingerprint: string | null = null;
  private resourceID = "";
  private editors: string[] = [];
  private savingText: Promise<void> | null = null;
  private textSaveQueued = false;
  private baseVersion = 0;
  private renamedTo = 0;
  private generation = "";
  private contentMode = false;
  private revision = 0;
  private dirty = false;
  private told = { dirty: false, loaded: false, base: 0, renamed: 0, hasWriter: false };
  private announceTimer: ReturnType<typeof setTimeout> | undefined;
  private quietSince = 0;
  private quietTimer: ReturnType<typeof setTimeout> | undefined;
  private textStopped = false;
  private outsideVersion: number | null = null;
  private answeredAt = -1;
  private check: { nonce: string; writer: string; timer: ReturnType<typeof setTimeout> } | null = null;
  private readonly released = new Set<string>();
  private restarting = false;
  private resolving: {
    version: number;
    timer: ReturnType<typeof setTimeout>;
    kept?: { name: string; fingerprint: string };
  } | null = null;
  private resolveTries = { version: 0, count: 0 };
  private readonly claims = new Map<string, Promise<boolean>>();

  override async onStart(): Promise<void> {
    await super.onStart();
    this.savedFingerprint = (await this.ctx.storage.get<string>(SAVED_KEY)) ?? null;
    this.resourceID = (await this.ctx.storage.get<string>("resourceID")) ?? "";
    const editors = await this.ctx.storage.get<unknown>(EDITORS_KEY);
    this.editors = Array.isArray(editors)
      ? editors.filter((editor): editor is string => typeof editor === "string" && editor.length > 0).slice(0, MAX_EDITORS)
      : [];
    this.baseVersion = (await this.ctx.storage.get<number>(BASE_KEY)) ?? 0;
    this.renamedTo = versionOf(await this.ctx.storage.get<number>(RENAMED_KEY)) ?? 0;
    this.generation = (await this.ctx.storage.get<string>(GENERATION_KEY)) ?? "";
    if (!this.generation) {
      this.generation = crypto.randomUUID();
      await this.ctx.storage.put(GENERATION_KEY, this.generation);
    }
    this.textStopped = (await this.ctx.storage.get<string>(OUTSIDE_KEY)) === this.generation;
    this.contentMode = (await this.ctx.storage.get<string>(CONTENT_MODE_KEY)) === this.generation;
    if (this.textStopped) this.outsideVersion = versionOf(await this.ctx.storage.get<number>(OUTSIDE_VERSION_KEY));
    if (this.savedFingerprint === null) {
      const legacy = await this.ctx.storage.get<string>(LEGACY_SAVED_KEY);
      const migrated = typeof legacy === "string" ? legacyFingerprint(this.document, legacy) : null;
      if (migrated) {
        this.savedFingerprint = await migrated;
        await this.ctx.storage.put(SAVED_KEY, this.savedFingerprint);
      }
    }
    this.dirty =
      this.loaded() && (await fingerprintOf(snapshotBytes(this.document))) !== this.savedFingerprint;
    this.told = { dirty: this.dirty, loaded: this.loaded(), base: this.baseVersion, renamed: this.renamedTo, hasWriter: false };
    this.quietSince = Date.now();
    this.document.on("update", () => this.changed());
  }

  override async onConnect(connection: Connection, ctx: ConnectionContext): Promise<void> {
    await super.onConnect(connection, ctx);
    const state = connection.state as FileSocketState | null;
    if (!state?.userID || this.getConnection(connection.id) === undefined) return;
    if (state.resourceID) this.resourceID = state.resourceID;
    const writes = !this.isReadOnly(connection);
    if (writes) this.noteEditor(state.userID);
    if (writes && (!this.writerIsHere() || this.writerIsUser(state.userID))) {
      this.setWriter(connection.id);
      this.tellAll();
      return;
    }
    this.tell(connection);
  }

  override onCustomMessage(connection: Connection, message: string): void {
    let said: {
      type?: unknown;
      fingerprint?: unknown;
      state_vector?: unknown;
      version?: unknown;
      renamed?: unknown;
      nonce?: unknown;
      kept?: unknown;
      kept_name?: unknown;
      content?: unknown;
    };
    try {
      said = JSON.parse(message) as typeof said;
    } catch {
      return;
    }
    if (this.isReadOnly(connection)) return;
    const fromWriter = connection.id === this.writer;
    if (said.type === "file_mode") {
      if (said.content === true) this.useContentVersions();
      return;
    }
    if (said.type === "file_saved" && said.renamed !== undefined) {
      this.renamed(said.renamed);
      return;
    }
    if (said.type === "file_saved") {
      this.saved(connection, said.fingerprint, said.state_vector, said.version);
      return;
    }
    if (said.type === "file_reload") {
      const kept = said.kept === true;
      void this.reload(connection, said.version, kept, said.fingerprint, kept ? nameOf(said.kept_name) : null).catch(
        () => undefined,
      );
      return;
    }
    if (said.type === "file_resolve") {
      this.resolve(connection, said.version);
      return;
    }
    if (said.type === "file_kept") {
      this.keptForResolve(connection, said.version, said.kept_name, said.fingerprint);
      return;
    }
    if (said.type === "file_outside" && this.isText()) {
      void this.stopTextSaves(connection, said.version).catch(() => undefined);
      return;
    }
    if (!fromWriter) return;
    if (said.type === "writer_ok") {
      this.writerAnswered(connection.id, said.nonce);
      return;
    }
    if (said.type === "file_outside") {
      if (this.resolving) this.endResolve();
      else this.broadcastCustomMessage(JSON.stringify({ type: "file_outside" }), connection);
      return;
    }
    if (said.type === "file_writer_release") {
      this.released.add(connection.id);
      this.handOver();
    }
  }

  private saved(connection: Connection, sent: unknown, vector: unknown, asked: unknown): void {
    const fromWriter = connection.id === this.writer;
    const fingerprint = validFingerprint(sent);
    if (fromWriter && (fingerprint !== null || typeof vector === "string")) {
      this.quietSince = Date.now();
      this.checkWriterIfQuiet();
    }
    const version = fingerprint === null ? null : versionOf(asked);
    if (version !== null && this.loaded() && version > this.baseVersion) void this.raiseBase(connection, version).catch(() => undefined);
    if (!fromWriter) {
      if (fingerprint) void this.savedElsewhere(fingerprint).catch(() => undefined);
      return;
    }
    if (fingerprint) {
      this.recordSaved(fingerprint);
      return;
    }
    if (typeof vector !== "string" || vector.length > MAX_LEGACY_VECTOR_LENGTH) return;
    void legacyFingerprint(this.document, vector)?.then((legacy) => this.recordSaved(legacy));
  }

  private async raiseBase(connection: Connection, version: number): Promise<void> {
    const generation = this.generation;
    if (!(await this.claimHolds("base", version, connection))) return;
    if (generation !== this.generation || this.restarting || this.isPurged() || !this.loaded() || version <= this.baseVersion) return;
    this.baseVersion = version;
    this.resolveTries.count = 0;
    void this.ctx.storage.put(BASE_KEY, version);
    if (this.resolving && version >= this.resolving.version) this.dropResolve();
    this.announceSoon();
  }

  private claimHolds(claim: VersionClaim, version: number, connection: Connection): Promise<boolean> {
    const fromWriter = connection.id === this.writer;
    const key = `${claim}:${version}:${fromWriter}:${(connection.state as FileSocketState | null)?.userID ?? ""}`;
    const pending = this.claims.get(key);
    if (pending) return pending;
    if (!fromWriter && this.claims.size >= MAX_CLAIMS) {
      this.log("file_claim_dropped", { claim, version });
      return Promise.resolve(false);
    }
    const checking = this.checkClaim(claim, version, connection, fromWriter).finally(() => this.claims.delete(key));
    this.claims.set(key, checking);
    return checking;
  }

  private async checkClaim(claim: VersionClaim, version: number, connection: Connection, fromWriter: boolean): Promise<boolean> {
    const [spaceID, itemID] = this.resourceID.split(":");
    const userID = (connection.state as FileSocketState | null)?.userID;
    const drive = spaceID && itemID && userID ? await readDriveVersions(this.ctx.storage, this.env, { spaceID, itemID, userID }) : null;
    if (drive === null) {
      this.log("file_claim_unverified", { claim, version, writer: fromWriter });
      return fromWriter;
    }
    if (
      drive !== "refused" &&
      versionClaimHolds(claim, version, drive, this.savedVersion()) &&
      (claim !== "base" || baseClaimantHolds(drive, userID, fromWriter))
    ) {
      return true;
    }
    this.log("file_claim_refused", { claim, version, writer: fromWriter });
    return false;
  }

  private useContentVersions(): void {
    if (this.contentMode || this.restarting) return;
    this.contentMode = true;
    this.log("file_content_mode", {});
    void this.ctx.storage.put(CONTENT_MODE_KEY, this.generation).catch(() => undefined);
  }

  private renamed(asked: unknown): void {
    const version = versionOf(asked);
    if (this.contentMode || version === null || !this.loaded() || version <= this.knownVersion()) return;
    this.renamedTo = version;
    void this.ctx.storage.put(RENAMED_KEY, version);
    this.announceSoon();
  }

  override async onClose(
    connection: Connection,
    code: number,
    reason: string,
    wasClean: boolean,
  ): Promise<void> {
    const closing = super.onClose(connection, code, reason, wasClean);
    this.released.delete(connection.id);
    if (this.writer === connection.id) this.handOver();
    await closing;
    if (this.isPurged() || this.openSocketCount() > 0) return;
    await this.settleText();
    await this.forgetIfDone();
  }

  override async onAlarm(): Promise<void> {
    await super.onAlarm();
    if (this.isPurged() || this.openSocketCount() > 0) return;
    await this.forgetIfDone();
  }

  private async forgetIfDone(): Promise<void> {
    if (this.openSocketCount() > 0 || this.savingText || this.keepsUnsaved()) return;
    await this.forget();
  }

  protected override async persist(): Promise<void> {
    let failure: unknown = null;
    try {
      await super.persist();
    } catch (error) {
      failure = error;
    }
    if (this.isText()) {
      try {
        await this.saveText();
      } catch (error) {
        this.log("file_text_save_failed", { error: String(error) });
        failure ??= error;
      }
    }
    if (failure !== null) throw failure;
  }

  protected override refusesUpdates(): boolean {
    return this.resolving !== null;
  }

  protected override hasUnsavedWork(): boolean {
    return super.hasUnsavedWork() || (this.isText() && this.dirty);
  }

  private keepsUnsaved(): boolean {
    return this.dirty;
  }

  private changed(): void {
    this.revision += 1;
    const was = this.dirty;
    this.dirty = this.loaded();
    if (this.dirty && !was) this.quietSince = Date.now();
    this.announceSoon();
    this.checkWriterIfQuiet();
  }

  private setWriter(id: string | null): void {
    if (this.writer === id) return;
    this.writer = id;
    this.quietSince = Date.now();
    this.answeredAt = -1;
    this.endCheck();
    this.checkWriterIfQuiet();
  }

  private endCheck(): void {
    if (!this.check) return;
    clearTimeout(this.check.timer);
    this.check = null;
  }

  private checkWriterIfQuiet(): void {
    if (this.quietTimer !== undefined) clearTimeout(this.quietTimer);
    this.quietTimer = undefined;
    if (
      this.check ||
      this.resolving !== null ||
      this.isText() ||
      !this.dirty ||
      this.writer === null ||
      this.isPurged() ||
      this.revision === this.answeredAt
    )
      return;
    const left = this.quietSince + WRITER_QUIET_MS - Date.now();
    if (left > 0) {
      this.quietTimer = setTimeout(() => this.checkWriterIfQuiet(), left);
      return;
    }
    const holder = this.getConnection(this.writer);
    if (!holder) return;
    const nonce = crypto.randomUUID();
    const writer = this.writer;
    this.check = {
      nonce,
      writer,
      timer: setTimeout(() => this.writerSilent(writer, nonce), WRITER_ANSWER_MS),
    };
    this.sendCustomMessage(holder, JSON.stringify({ type: "writer_check", nonce }));
  }

  private writerAnswered(id: string, nonce: unknown): void {
    if (typeof nonce !== "string" || nonce.length > MAX_NONCE_LENGTH) return;
    if (!this.check || this.check.writer !== id || this.check.nonce !== nonce) return;
    this.endCheck();
    this.answeredAt = this.revision;
    this.quietSince = Date.now();
    this.checkWriterIfQuiet();
  }

  private writerSilent(writer: string, nonce: string): void {
    if (!this.check || this.check.nonce !== nonce) return;
    this.check = null;
    if (this.writer !== writer) return;
    const next = this.nextWriter(writer);
    this.log("file_writer_silent", { moved: next !== null });
    if (next === null) {
      this.answeredAt = this.revision;
      this.quietSince = Date.now();
      return;
    }
    this.setWriter(next);
    this.tellAll();
  }

  private nextWriter(skip: string | null): string | null {
    for (const other of this.getConnections()) {
      if (other.id === skip || this.released.has(other.id) || this.isReadOnly(other)) continue;
      if ((other.state as FileSocketState | null)?.userID) return other.id;
    }
    return null;
  }

  private recordSaved(fingerprint: string): void {
    this.savedFingerprint = fingerprint;
    void this.ctx.storage.put(SAVED_KEY, fingerprint);
    void this.settleDirty();
  }

  private async settleDirty(): Promise<void> {
    const revision = this.revision;
    const current = await fingerprintOf(snapshotBytes(this.document));
    if (revision !== this.revision) return;
    this.dirty = this.loaded() && current !== this.savedFingerprint;
    this.announceSoon();
    this.checkWriterIfQuiet();
  }

  private async savedElsewhere(sent: string): Promise<void> {
    if (!this.loaded()) return;
    const generation = this.generation;
    const revision = this.revision;
    const current = await fingerprintOf(snapshotBytes(this.document));
    if (generation !== this.generation || revision !== this.revision || this.isPurged() || !this.loaded()) return;
    if (sent === current) this.recordSaved(sent);
  }

  private async stopTextSaves(connection: Connection, asked: unknown): Promise<void> {
    const version = versionOf(asked);
    if (version === null || this.textStopped || this.isPurged() || !this.loaded()) return;
    const generation = this.generation;
    if (this.savingText) await this.savingText.catch(() => undefined);
    if (generation !== this.generation || this.textStopped || !this.loaded()) return;
    if (version <= this.knownVersion()) return;
    if (!(await this.claimHolds("outside", version, connection))) return;
    if (generation !== this.generation || this.textStopped || this.isPurged() || !this.loaded() || version <= this.knownVersion()) return;
    await this.markTextStopped(version);
  }

  private async markTextStopped(version: number | null): Promise<void> {
    if (this.textStopped) return;
    this.textStopped = true;
    this.outsideVersion = version;
    this.log("file_text_outside", { version });
    await this.ctx.storage.put({
      [OUTSIDE_KEY]: this.generation,
      ...(version === null ? {} : { [OUTSIDE_VERSION_KEY]: version }),
    });
    this.tellAll();
  }

  private savedVersion(): number {
    return Math.max(this.baseVersion, versionOf(this.document.getMap<unknown>(FILE_META).get("loaded")) ?? 0);
  }

  private knownVersion(): number {
    return this.contentMode ? this.savedVersion() : Math.max(this.savedVersion(), this.renamedTo);
  }

  private announceSoon(): void {
    if (this.announceTimer !== undefined) return;
    this.announceTimer = setTimeout(() => {
      this.announceTimer = undefined;
      this.announce();
    }, ANNOUNCE_AFTER_MS);
  }

  private announce(): void {
    const loaded = this.loaded();
    if (
      loaded === this.told.loaded &&
      this.dirty === this.told.dirty &&
      this.baseVersion === this.told.base &&
      this.renamedTo === this.told.renamed &&
      this.writerIsHere() === this.told.hasWriter
    )
      return;
    this.tellAll();
  }

  private handOver(): void {
    const leaving = this.writer;
    this.setWriter(null);
    this.setWriter(this.nextWriter(leaving));
    this.tellAll();
  }

  private writerIsHere(): boolean {
    return this.writer !== null && this.getConnection(this.writer) !== undefined;
  }

  private writerIsUser(userID: string): boolean {
    if (this.writer === null) return false;
    const holder = this.getConnection(this.writer);
    return (holder?.state as FileSocketState | null)?.userID === userID;
  }

  private mayReload(asker: string): boolean {
    return this.writer === asker || (this.isText() && this.getConnection(asker) !== undefined);
  }

  private async reload(connection: Connection, asked: unknown, kept: boolean, sent: unknown, keptName: string | null): Promise<void> {
    const asker = connection.id;
    const version = versionOf(asked);
    const keptAs = kept ? validFingerprint(sent) : null;
    if (version === null || this.isPurged() || !this.loaded() || this.savingText || this.restarting) return;
    if (this.resolving && !kept) return;
    if (kept ? keptAs === null || this.writer !== asker : this.dirty || !this.mayReload(asker)) return;
    if (!this.textStopped && version <= this.knownVersion()) return;
    if (kept && !(await this.claimHolds("kept", version, connection))) return;
    const revision = this.revision;
    const current = await fingerprintOf(snapshotBytes(this.document));
    if (this.isPurged() || !this.loaded() || this.savingText || this.restarting || revision !== this.revision) return;
    if (kept) {
      if (this.writer !== asker || current !== keptAs) return;
    } else if (this.dirty || !this.mayReload(asker) || current !== this.savedFingerprint) return;
    await this.ctx.blockConcurrencyWhile(() => this.restart(version, kept, current, keptName, RELOAD_SAVE_WAIT_MS));
    setTimeout(() => this.ctx.abort("file_room_reloaded"), RELOAD_ABORT_DELAY_MS);
  }

  private async restart(
    version: number,
    kept: boolean,
    current: string,
    keptName: string | null,
    stopWaitMs: number,
  ): Promise<void> {
    const generation = crypto.randomUUID();
    this.generation = generation;
    this.contentMode = false;
    if (this.resolving) clearTimeout(this.resolving.timer);
    this.endCheck();
    if (this.quietTimer !== undefined) clearTimeout(this.quietTimer);
    this.quietTimer = undefined;
    for (const connection of this.getConnections()) {
      this.sendCustomMessage(
        connection,
        JSON.stringify({
          type: "file_writer",
          writer: false,
          dirty: false,
          read_only: this.isReadOnly(connection),
          loaded: false,
          generation,
          base: 0,
          has_writer: false,
          kept,
          fingerprint: current,
          ...(keptName ? { kept_name: keptName } : {}),
        }),
      );
    }
    clearDocument(this.document);
    await this.stopSaves(stopWaitMs);
    if (this.savingText) await bounded(this.savingText, stopWaitMs);
    const stored = await this.ctx.storage.list({ prefix: "doc:" });
    const keys = [...stored.keys(), SAVED_KEY, LEGACY_SAVED_KEY, BASE_KEY, RENAMED_KEY, OUTSIDE_KEY, OUTSIDE_VERSION_KEY, CONTENT_MODE_KEY];
    for (let start = 0; start < keys.length; start += DELETE_BATCH) {
      await this.ctx.storage.delete(keys.slice(start, start + DELETE_BATCH));
    }
    await this.ctx.storage.put(GENERATION_KEY, generation);
    await this.ctx.storage.sync();
    this.restarting = true;
    for (const connection of [...this.getConnections()]) closeSocket(connection, "file_reloaded");
    this.log("file_room_reloaded", { version, kept });
  }

  private resolve(connection: Connection, asked: unknown): void {
    const version = versionOf(asked);
    if (this.resolving) {
      this.sendCustomMessage(connection, JSON.stringify({ type: "file_resolving", version: this.resolving.version }));
      return;
    }
    if (version !== null && version > this.resolveTries.version) this.resolveTries = { version, count: 0 };
    if (
      version === null ||
      this.isPurged() ||
      this.restarting ||
      !this.loaded() ||
      connection.id !== this.writer ||
      this.resolveTries.count >= MAX_RESOLVE_TRIES ||
      (!this.textStopped && version <= this.knownVersion())
    ) {
      this.sendCustomMessage(connection, JSON.stringify({ type: "file_resolve_declined", version: version ?? 0 }));
      return;
    }
    this.resolveTries.count += 1;
    this.resolving = { version, timer: setTimeout(() => this.resolveTimedOut(version), RESOLVE_BOUND_MS) };
    this.endCheck();
    if (this.quietTimer !== undefined) clearTimeout(this.quietTimer);
    this.quietTimer = undefined;
    this.log("file_resolving", { version, text: this.isText() });
    const message = JSON.stringify({ type: "file_resolving", version });
    for (const other of this.getConnections()) this.sendCustomMessage(other, message);
    if (this.isText()) void this.resolveText(version).catch(() => this.endResolve());
  }

  private keptForResolve(connection: Connection, asked: unknown, sentName: unknown, sent: unknown): void {
    const version = versionOf(asked);
    const name = nameOf(sentName);
    const fingerprint = validFingerprint(sent);
    const resolving = this.resolving;
    if (!resolving || resolving.kept || this.isText()) return;
    if (version !== resolving.version || name === null || fingerprint === null) return;
    void this.claimHolds("kept", version, connection)
      .then((holds) => {
        if (!holds || this.resolving !== resolving || resolving.kept || this.restarting) return;
        resolving.kept = { name, fingerprint };
        this.tellAll();
      })
      .catch(() => undefined);
  }

  private askResend(): void {
    const encoder = encoding.createEncoder();
    encoding.writeVarUint(encoder, MESSAGE_SYNC);
    encoding.writeVarUint(encoder, SYNC_STEP_1);
    encoding.writeVarUint8Array(encoder, Y.encodeStateVector(this.document));
    const message = encoding.toUint8Array(encoder);
    for (const connection of this.getConnections()) {
      if (this.isReadOnly(connection)) continue;
      try {
        connection.send(message);
      } catch {
        this.log("file_resend_ask_failed", {});
      }
    }
  }

  private resolveTimedOut(version: number): void {
    if (this.resolving?.version !== version) return;
    this.log("file_resolve_timed_out", { version });
    this.endResolve();
  }

  private stillResolving(version: number, generation: string): boolean {
    return (
      this.resolving?.version === version &&
      this.generation === generation &&
      !this.isPurged() &&
      !this.restarting &&
      this.loaded()
    );
  }

  private async resolveText(version: number): Promise<void> {
    const generation = this.generation;
    if (this.savingText) await bounded(this.savingText, RESOLVE_SAVE_WAIT_MS);
    if (!this.stillResolving(version, generation)) return;
    if (!this.textStopped && version <= this.savedVersion()) {
      this.dropResolve();
      return;
    }
    let restarted = false;
    await this.ctx.blockConcurrencyWhile(async () => {
      const target = this.textTarget();
      if (!target || !this.stillResolving(version, generation)) return;
      const text = this.document.getText(FILE_TEXT).toString();
      const current = await fingerprintOf(snapshotBytes(this.document));
      let name: string | null = null;
      try {
        name = await this.keepTextCopy(target, text, Date.now() + RESOLVE_COPY_MS);
      } catch (error) {
        this.log("file_text_copy_failed", { error: String(error) });
      }
      if (name === null || !this.stillResolving(version, generation)) return;
      this.log("file_text_kept", { resolving: true });
      await this.restart(version, true, current, nameOf(name), RESOLVE_STOP_WAIT_MS);
      restarted = true;
    });
    if (restarted) {
      setTimeout(() => this.ctx.abort("file_room_reloaded"), RELOAD_ABORT_DELAY_MS);
      return;
    }
    if (this.resolving?.version === version && this.generation === generation) this.endResolve();
  }

  private endResolve(): void {
    const resolving = this.resolving;
    if (!resolving || this.restarting) return;
    clearTimeout(resolving.timer);
    this.resolving = null;
    this.askResend();
    if (this.isText()) {
      if (this.textStopped) this.tellAll();
      else void this.markTextStopped(resolving.version).catch(() => undefined);
      if (this.openSocketCount() === 0) void this.settleText().then(() => this.forgetIfDone());
      return;
    }
    const outside = JSON.stringify({ type: "file_outside" });
    for (const connection of this.getConnections()) this.sendCustomMessage(connection, outside);
    this.tellAll();
    this.checkWriterIfQuiet();
  }

  private dropResolve(): void {
    const resolving = this.resolving;
    if (!resolving || this.restarting) return;
    clearTimeout(resolving.timer);
    this.resolving = null;
    this.askResend();
    this.log("file_resolve_dropped", { version: resolving.version });
    this.tellAll();
    this.checkWriterIfQuiet();
  }

  private noteEditor(userID: string): void {
    if (this.editors[0] === userID) return;
    this.editors = [userID, ...this.editors.filter((editor) => editor !== userID)].slice(0, MAX_EDITORS);
    void this.ctx.storage.put(EDITORS_KEY, this.editors).catch(() => undefined);
  }

  private tellAll(): void {
    this.told = {
      dirty: this.dirty,
      loaded: this.loaded(),
      base: this.baseVersion,
      renamed: this.renamedTo,
      hasWriter: this.writerIsHere(),
    };
    for (const connection of this.getConnections()) this.tell(connection);
  }

  private tell(connection: Connection): void {
    this.sendCustomMessage(
      connection,
      JSON.stringify({
        type: "file_writer",
        writer: connection.id === this.writer,
        dirty: this.dirty,
        read_only: this.isReadOnly(connection),
        loaded: this.loaded(),
        generation: this.generation,
        base: this.baseVersion,
        ...(this.renamedTo > 0 ? { renamed: this.renamedTo } : {}),
        has_writer: this.writerIsHere(),
        ...(this.resolving
          ? {
              resolving: this.resolving.version,
              ...(this.resolving.kept
                ? { kept_name: this.resolving.kept.name, kept_fingerprint: this.resolving.kept.fingerprint }
                : {}),
            }
          : {}),
        ...(this.textStopped
          ? { outside: true, ...(this.outsideVersion === null ? {} : { drive_version: this.outsideVersion }) }
          : {}),
      }),
    );
  }

  private isText(): boolean {
    return this.resourceID.split(":").length === 2;
  }

  private loaded(): boolean {
    return this.document.getMap<unknown>(FILE_META).get("loaded") !== undefined;
  }

  private async settleText(): Promise<void> {
    if (!this.isText()) return;
    try {
      await this.saveText();
    } catch (error) {
      this.log("file_text_save_failed", { error: String(error) });
      this.scheduleSaveRetry();
    }
  }

  private saveText(): Promise<void> {
    if (this.savingText) {
      this.textSaveQueued = true;
      return this.savingText;
    }
    const run = (async () => {
      do {
        this.textSaveQueued = false;
        await this.saveTextOnce();
      } while (this.textSaveQueued && this.dirty);
    })();
    this.savingText = run.finally(() => {
      this.savingText = null;
    });
    return this.savingText;
  }

  private textTarget(): TextTarget | null {
    const loadedFrom = this.document.getMap<unknown>(FILE_META).get("loaded");
    if (typeof loadedFrom !== "number" || !Number.isFinite(loadedFrom)) return null;
    const [spaceID, itemID] = this.resourceID.split(":");
    const holder = this.writer ? this.getConnection(this.writer) : undefined;
    const userID = (holder?.state as FileSocketState | null)?.userID || this.editors[0];
    if (!spaceID || !itemID || !userID) return null;
    return { spaceID, itemID, userID, base: this.baseVersion || loadedFrom };
  }

  private async saveTextOnce(): Promise<void> {
    if (!this.dirty || this.isPurged() || this.resolving) return;
    if (this.textStopped) {
      if (this.openSocketCount() === 0) await this.keepStoppedText();
      return;
    }
    const target = this.textTarget();
    if (!target) return;
    const generation = this.generation;
    const snapshot = snapshotBytes(this.document);
    const text = this.document.getText(FILE_TEXT).toString();
    let fingerprint = await fingerprintOf(snapshot);
    const { status, answer } = await postRoomCallbackAnswer(this.ctx.storage, this.env, FILE_TEXTS_PATH, {
      space_id: target.spaceID,
      item_id: target.itemID,
      user_id: target.userID,
      text,
      base_version: target.base,
    });
    if (generation !== this.generation || this.restarting) return;
    if (status === 409) {
      await this.markTextStopped(versionOf((answer as { version?: unknown } | null)?.version));
      if (this.openSocketCount() === 0) await this.keepStoppedText();
      return;
    }
    if (status < 200 || status >= 300) throw new Error(`file_text_callback_${status}`);
    const version = (answer as { version?: unknown } | null)?.version;
    if (typeof version === "number" && Number.isFinite(version)) {
      this.baseVersion = Math.max(this.baseVersion, version);
      this.resolveTries.count = 0;
      const meta = this.document.getMap<unknown>(FILE_META);
      const recorded = meta.get("saved");
      if (typeof recorded !== "number" || recorded < version) {
        const clean = sameBytes(snapshotBytes(this.document), snapshot);
        this.document.transact(() => meta.set("saved", version));
        if (clean) fingerprint = await fingerprintOf(snapshotBytes(this.document));
      }
    }
    if (generation !== this.generation || this.restarting) return;
    this.savedFingerprint = fingerprint;
    await this.ctx.storage.put({ [SAVED_KEY]: fingerprint, [BASE_KEY]: this.baseVersion });
    await this.settleDirty();
  }

  private async keepStoppedText(): Promise<void> {
    const target = this.textTarget();
    if (!target) return;
    const generation = this.generation;
    const snapshot = snapshotBytes(this.document);
    const text = this.document.getText(FILE_TEXT).toString();
    const fingerprint = await fingerprintOf(snapshot);
    if ((await this.keepTextCopy(target, text)) === null) throw new Error("file_text_copy_refused");
    if (generation !== this.generation) return;
    this.log("file_text_kept", {});
    this.savedFingerprint = fingerprint;
    await this.ctx.storage.put(SAVED_KEY, fingerprint);
    await this.settleDirty();
  }

  private async keepTextCopy(target: TextTarget, text: string, deadline?: number): Promise<string | null> {
    for (const userID of new Set([target.userID, ...this.editors])) {
      const left = deadline === undefined ? undefined : deadline - Date.now();
      if (left !== undefined && left < 1_000) return null;
      const { status, answer } = await postRoomCallbackAnswer(
        this.ctx.storage,
        this.env,
        FILE_TEXTS_PATH,
        {
          space_id: target.spaceID,
          item_id: target.itemID,
          user_id: userID,
          text,
          base_version: target.base,
          conflict_copy: true,
        },
        left,
      );
      if (status >= 200 && status < 300) return nameOf((answer as { name?: unknown } | null)?.name) ?? "";
      if (status !== 403 && status !== 404) return null;
    }
    return null;
  }

  private async forget(): Promise<void> {
    if (this.restarting) return;
    await this.ctx.blockConcurrencyWhile(async () => {
      if (this.openSocketCount() > 0 || this.savingText || this.keepsUnsaved()) return;
      const stored = await this.ctx.storage.list({ prefix: "doc:" });
      const tickets = await this.ctx.storage.list({ prefix: "jti:" });
      const keys = [
        ...stored.keys(),
        ...tickets.keys(),
        "resourceID",
        "spaceID",
        "aclVersion",
        SAVED_KEY,
        LEGACY_SAVED_KEY,
        BASE_KEY,
        RENAMED_KEY,
        GENERATION_KEY,
        OUTSIDE_KEY,
        OUTSIDE_VERSION_KEY,
        EDITORS_KEY,
        KEEP_DELETES_KEY,
      ];
      for (let start = 0; start < keys.length; start += DELETE_BATCH) {
        await this.ctx.storage.delete(keys.slice(start, start + DELETE_BATCH));
      }
      await this.ctx.storage.sync();
      this.log("file_room_forgotten", {});
      this.ctx.abort("file_room_forgotten");
    });
  }
}
