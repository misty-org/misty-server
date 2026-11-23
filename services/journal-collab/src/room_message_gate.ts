import type * as Y from "yjs";

import {
  DOCUMENT_WARNING_BYTES,
  MAX_DOCUMENT_BYTES,
  withProjectedDocument,
} from "./document_persistence";

type UpdateGate =
  | { status: "blocked"; projectedBytes: number }
  | { status: "warning"; projectedBytes: number }
  | null;

export function gateDocumentUpdate(document: Y.Doc, message: ArrayBuffer): UpdateGate {
  return withProjectedDocument(document, new Uint8Array(message), (_projected, projectedBytes): UpdateGate => {
    if (projectedBytes > MAX_DOCUMENT_BYTES) return { status: "blocked", projectedBytes };
    if (projectedBytes >= DOCUMENT_WARNING_BYTES) return { status: "warning", projectedBytes };
    return null;
  });
}
