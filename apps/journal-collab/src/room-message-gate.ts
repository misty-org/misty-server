import type * as Y from "yjs";

import {
  DOCUMENT_WARNING_BYTES,
  MAX_DOCUMENT_BYTES,
  projectedDocumentBytes,
} from "./document-persistence";

export type UpdateGate =
  | { status: "blocked"; projectedBytes: number }
  | { status: "warning"; projectedBytes: number }
  | null;

/**
 * Decides whether a binary Yjs update would push the document past its size
 * limits, before y-partyserver applies it.
 */
export function gateDocumentUpdate(document: Y.Doc, message: ArrayBuffer): UpdateGate {
  const projectedBytes = projectedDocumentBytes(document, new Uint8Array(message));
  if (projectedBytes === null) return null;
  if (projectedBytes > MAX_DOCUMENT_BYTES) return { status: "blocked", projectedBytes };
  if (projectedBytes >= DOCUMENT_WARNING_BYTES) return { status: "warning", projectedBytes };
  return null;
}
