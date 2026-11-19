import type * as Y from "yjs";

import {
  DOCUMENT_WARNING_BYTES,
  MAX_DOCUMENT_BYTES,
  withProjectedDocument,
} from "./document-persistence";

type UpdateGate =
  | { status: "blocked"; projectedBytes: number; code?: string }
  | { status: "warning"; projectedBytes: number }
  | null;

/**
 * A limit on what a document holds besides its size: a deck's slides, a workbook's filled cells.
 * An update that would take the count past the limit, and higher than it is now, is refused, so
 * an item already past it (its Space dropped a level) stays editable and may shrink.
 */
export interface GrowthLimit {
  limit: number;
  count(document: Y.Doc): number;
  /** The status code the refused client is told. */
  code: string;
}

/**
 * Decides whether a binary Yjs update would push the document past its size
 * limits, or past its growth limit, before y-partyserver applies it.
 */
export function gateDocumentUpdate(
  document: Y.Doc,
  message: ArrayBuffer,
  growth?: GrowthLimit,
): UpdateGate {
  return withProjectedDocument(document, new Uint8Array(message), (projected, projectedBytes): UpdateGate => {
    if (projectedBytes > MAX_DOCUMENT_BYTES) return { status: "blocked", projectedBytes };
    if (growth) {
      const next = growth.count(projected);
      if (next > growth.limit && next > growth.count(document)) {
        return { status: "blocked", projectedBytes, code: growth.code };
      }
    }
    if (projectedBytes >= DOCUMENT_WARNING_BYTES) return { status: "warning", projectedBytes };
    return null;
  });
}
