import { MAX_DOCUMENT_BYTES } from "./document_persistence";

type DocumentStatus = "saved" | "warning" | "blocked" | "error";

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
