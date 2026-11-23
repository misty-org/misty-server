import type { Connection, ConnectionContext } from "partyserver";
import * as Y from "yjs";

import { PersistentDocumentRoom } from "./document_room";
import { postRoomCallback } from "./room_callback";

const SAVED_KEY = "file:saved_vector";
const BASE_KEY = "file:base_version";
const FILE_META = "misty:file";
const FILE_TEXT = "text";
const DELETE_BATCH = 128;

interface FileSocketState {
  userID?: string;
  resourceID?: string;
}

function base64(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

export class FileRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "file" as const;
  private writer: string | null = null;
  private savedVector: string | null = null;
  private resourceID = "";
  private lastEditor = "";
  private savingText: Promise<void> | null = null;
  private baseVersion = 0;

  override async onStart(): Promise<void> {
    await super.onStart();
    this.savedVector = (await this.ctx.storage.get<string>(SAVED_KEY)) ?? null;
    this.resourceID = (await this.ctx.storage.get<string>("resourceID")) ?? "";
    this.baseVersion = (await this.ctx.storage.get<number>(BASE_KEY)) ?? 0;
  }

  override async onConnect(connection: Connection, ctx: ConnectionContext): Promise<void> {
    await super.onConnect(connection, ctx);
    const state = connection.state as FileSocketState | null;
    if (!state?.userID) return;
    if (state.resourceID) this.resourceID = state.resourceID;
    const writes = !this.isReadOnly(connection);
    if (writes) this.lastEditor = state.userID;
    if (writes && !this.writerIsHere()) this.writer = connection.id;
    this.tell(connection);
  }

  override onCustomMessage(connection: Connection, message: string): void {
    let said: { type?: unknown; state_vector?: unknown };
    try {
      said = JSON.parse(message) as typeof said;
    } catch {
      return;
    }
    if (said.type !== "file_saved" || connection.id !== this.writer) return;
    if (typeof said.state_vector !== "string" || said.state_vector.length > 64 * 1024) return;
    this.savedVector = said.state_vector;
    void this.ctx.storage.put(SAVED_KEY, said.state_vector);
  }

  override async onClose(
    connection: Connection,
    code: number,
    reason: string,
    wasClean: boolean,
  ): Promise<void> {
    await super.onClose(connection, code, reason, wasClean);
    const others = [...this.getConnections()].filter((other) => other.id !== connection.id);
    if (this.writer === connection.id) {
      this.writer = null;
      const next = others.find(
        (other) => !this.isReadOnly(other) && Boolean((other.state as FileSocketState | null)?.userID),
      );
      if (next) {
        this.writer = next.id;
        this.tell(next);
      }
    }
    if (others.length) return;
    if (this.isText()) {
      try {
        await this.saveText();
      } catch (error) {
        this.log("file_text_save_failed", { error: String(error) });
      }
    }
    if (this.dirty()) return;
    await this.forget();
  }

  override async onSave(): Promise<void> {
    await super.onSave();
    if (!this.isText()) return;
    try {
      await this.saveText();
    } catch (error) {
      this.log("file_text_save_failed", { error: String(error) });
    }
  }

  private writerIsHere(): boolean {
    return this.writer !== null && this.getConnection(this.writer) !== undefined;
  }

  private tell(connection: Connection): void {
    this.sendCustomMessage(
      connection,
      JSON.stringify({ type: "file_writer", writer: connection.id === this.writer, dirty: this.dirty() }),
    );
  }

  private isText(): boolean {
    return this.resourceID.split(":").length === 2;
  }

  private vector(): string {
    return base64(Y.encodeStateVector(this.document));
  }

  private loaded(): boolean {
    return this.document.getMap<unknown>(FILE_META).get("loaded") !== undefined;
  }

  private dirty(): boolean {
    return this.loaded() && this.vector() !== this.savedVector;
  }

  private saveText(): Promise<void> {
    if (this.savingText) return this.savingText;
    if (!this.dirty()) return Promise.resolve();
    const run = (async () => {
      const vector = this.vector();
      const [spaceID, itemID] = this.resourceID.split(":");
      const holder = this.writer ? this.getConnection(this.writer) : undefined;
      const userID = (holder?.state as FileSocketState | null)?.userID || this.lastEditor;
      if (!spaceID || !itemID || !userID) return;
      const loadedFrom = Number(this.document.getMap<unknown>(FILE_META).get("loaded") ?? 0);
      const answer = (await postRoomCallback(
        this.ctx.storage,
        this.env,
        "/internal/journal/file-texts",
        {
          space_id: spaceID,
          item_id: itemID,
          user_id: userID,
          text: this.document.getText(FILE_TEXT).toString(),
          base_version: this.baseVersion || loadedFrom,
        },
        "file_text_callback",
      )) as { version?: unknown } | null;
      this.savedVector = vector;
      if (typeof answer?.version === "number") this.baseVersion = answer.version;
      await this.ctx.storage.put({ [SAVED_KEY]: vector, [BASE_KEY]: this.baseVersion });
    })();
    this.savingText = run.finally(() => {
      this.savingText = null;
    });
    return this.savingText;
  }

  private async forget(): Promise<void> {
    const stored = await this.ctx.storage.list({ prefix: "doc:" });
    const keys = [...stored.keys(), SAVED_KEY, BASE_KEY];
    for (let start = 0; start < keys.length; start += DELETE_BATCH) {
      await this.ctx.storage.delete(keys.slice(start, start + DELETE_BATCH));
    }
    this.log("file_room_forgotten", {});
    this.ctx.abort("file_room_forgotten");
  }
}
