import * as Y from "yjs";

import {
  DOCUMENT_WARNING_BYTES,
  readDocumentSnapshot,
  writeDocumentSnapshot,
} from "./document-persistence";
import { documentStatusMessage } from "./document-status";

export interface RoomSnapshotContext {
  storage: DurableObjectStorage;
  document: Y.Doc;
  /** Sends a status message to every socket in the room. */
  broadcast(message: string): void;
  /** Pushes the note projection after a save, when the room supports it. */
  publishNoteProjection?: () => Promise<void>;
  log(event: string, details: Record<string, unknown>): void;
}

/** Applies the stored snapshot, if any, to the room's document. */
export async function loadRoomSnapshot(room: RoomSnapshotContext): Promise<void> {
  const snapshot = await readDocumentSnapshot(room.storage);
  if (snapshot) {
    Y.applyUpdate(room.document, snapshot.update);
    if (snapshot.source !== "current") {
      room.log("document_snapshot_recovered", { source: snapshot.source });
    }
  }
}

/** Writes the whole document as one chunked snapshot and tells the room how it went. */
export async function saveRoomSnapshot(room: RoomSnapshotContext): Promise<void> {
  const update = Y.encodeStateAsUpdate(room.document);
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
