import * as Y from "yjs";

import {
  DOCUMENT_WARNING_BYTES,
  readDocumentSnapshot,
  storedSnapshotExists,
  writeDocumentSnapshot,
} from "./document_persistence";
import { maybeRecordHistory } from "./document_history";
import { documentStatusMessage } from "./document_status";
import { readRoomLimits } from "./room_limits";

export interface RoomSnapshotContext {
  storage: DurableObjectStorage;
  document: Y.Doc;
  broadcast(message: string): void;
  publishNoteProjection?: () => Promise<void>;
  keepsHistory?: boolean;
  measured?(bytes: number): void;
  log(event: string, details: Record<string, unknown>): void;
}

export async function loadRoomSnapshot(room: RoomSnapshotContext): Promise<void> {
  const snapshot = await readDocumentSnapshot(room.storage);
  if (snapshot) {
    Y.applyUpdate(room.document, snapshot.update);
    if (snapshot.source !== "current") {
      room.log("document_snapshot_recovered", { source: snapshot.source });
    }
    return;
  }
  if (await storedSnapshotExists(room.storage)) {
    room.log("document_snapshot_load_failed", { starts_empty: true });
  }
}

export async function saveRoomSnapshot(room: RoomSnapshotContext): Promise<void> {
  const update = Y.encodeStateAsUpdate(room.document);
  room.measured?.(update.byteLength);
  if (room.keepsHistory) {
    try {
      const limits = await readRoomLimits(room.storage);
      await maybeRecordHistory(room.storage, Date.now(), limits.historyDays);
    } catch (historyError) {
      room.log("history_record_failed", { error: String(historyError) });
    }
  }
  try {
    const manifest = await writeDocumentSnapshot(room.storage, update);
    room.broadcast(
      documentStatusMessage(
        manifest.byteLength >= DOCUMENT_WARNING_BYTES ? "warning" : "saved",
        manifest.byteLength,
      ),
    );
    if (room.publishNoteProjection) {
      try {
        await room.publishNoteProjection();
      } catch (projectionError) {
        room.log("note_projection_failed", { error: String(projectionError) });
      }
    }
  } catch (error) {
    const code =
      error instanceof Error && error.message === "document_limit_exceeded"
        ? "document_limit_exceeded"
        : "document_persistence_failed";
    room.broadcast(documentStatusMessage("error", update.byteLength, code));
    room.log(code, { document_bytes: update.byteLength });
    throw error;
  }
}
