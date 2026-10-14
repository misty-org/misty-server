import { MAX_DOCUMENT_BYTES } from "./document-persistence";

export type DocumentStatus = "saved" | "warning" | "blocked" | "error";

/** The document_status frame sent to clients, as a JSON string. */
export function documentStatusMessage(
  status: DocumentStatus,
  documentBytes: number,
  code?: string,
): string {
  return JSON.stringify({
    type: "document_status",
    status,
    code,
    document_bytes: documentBytes,
    maximum_bytes: MAX_DOCUMENT_BYTES,
  });
}
