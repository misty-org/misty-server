import * as Y from "yjs";

import {
  DOCUMENT_WARNING_BYTES,
  MAX_DOCUMENT_BYTES,
  incomingUpdate,
  projectedDocumentBytes,
} from "./document_persistence";

type UpdateGate =
  | { status: "blocked"; projectedBytes: number }
  | { status: "warning"; projectedBytes: number }
  | null;

const REMEASURE_AFTER_BYTES = 256 * 1024;

export class DocumentSizeGate {
  private measuredBytes = 0;
  private appliedSince = 0;

  constructor(private readonly document: Y.Doc) {
    document.on("update", (update: Uint8Array) => {
      this.appliedSince += update.byteLength;
    });
  }

  measured(bytes: number): void {
    this.measuredBytes = bytes;
    this.appliedSince = 0;
  }

  measure(): number {
    const bytes = Y.encodeStateAsUpdate(this.document).byteLength;
    this.measured(bytes);
    return bytes;
  }

  check(message: ArrayBuffer): UpdateGate {
    const bytes = new Uint8Array(message);
    const update = incomingUpdate(bytes);
    if (!update) return null;
    let estimate = this.measuredBytes + this.appliedSince + update.byteLength;
    if (estimate < DOCUMENT_WARNING_BYTES) return null;
    if (estimate > MAX_DOCUMENT_BYTES || this.appliedSince >= REMEASURE_AFTER_BYTES) {
      estimate = this.measure() + update.byteLength;
    }
    if (estimate > MAX_DOCUMENT_BYTES) {
      const projected = projectedDocumentBytes(this.document, bytes);
      if (projected === null) return null;
      if (projected > MAX_DOCUMENT_BYTES) return { status: "blocked", projectedBytes: projected };
      estimate = projected;
    }
    return estimate >= DOCUMENT_WARNING_BYTES ? { status: "warning", projectedBytes: estimate } : null;
  }
}
