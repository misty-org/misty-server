import { ConnectionTokenError } from "../connections/token-broker.js";
import { SpaceError } from "../spaces/model.js";

export class MailError extends Error {
  constructor(readonly code: "mail_invalid_request" | "mail_connection_not_found" | "mail_capability_required" | "mail_provider_unsupported" |
    "mail_provider_mailbox_unavailable" | "mail_provider_item_not_found" | "mail_provider_rate_limited" | "mail_provider_authorization_failed" |
    "mail_provider_unavailable" | "mail_response_too_large" | "mail_body_too_large" | "mail_draft_busy" | "mail_draft_reconciliation_required" | "mail_confirmation_required", readonly providerStatus?: number) { super(code); this.name = "MailError"; }
}
export function mailError(error: unknown): MailError {
  if (error instanceof MailError) return error;
  if (error instanceof SpaceError && error.code === "not_found") return new MailError("mail_connection_not_found");
  if (error instanceof SpaceError && error.code === "invalid_request") return new MailError("mail_invalid_request");
  if (error instanceof ConnectionTokenError) {
    if (error.code === "provider_unsupported") return new MailError("mail_provider_unsupported");
    if (error.code === "capability_required") return new MailError("mail_capability_required");
  }
  return new MailError("mail_provider_unavailable");
}
export const mailStatus = (error: MailError) => {
  switch (error.code) {
    case "mail_invalid_request": return 400;
    case "mail_connection_not_found": case "mail_provider_item_not_found": return 404;
    case "mail_capability_required": return 403;
    case "mail_draft_busy": case "mail_draft_reconciliation_required": case "mail_confirmation_required": return 409;
    case "mail_provider_unsupported": case "mail_provider_mailbox_unavailable": return 422;
    case "mail_provider_rate_limited": return 429;
    case "mail_response_too_large": case "mail_body_too_large": return 413;
    default: return 424;
  }
};

/** Classify bounded provider data, then discard messages/codes that may be private. */
export function providerMailError(status: number, raw: unknown) {
  const record = raw && typeof raw === "object" && "error" in raw ? raw.error : null;
  const code = record && typeof record === "object" && "code" in record && typeof record.code === "string" ? record.code.trim().toLowerCase() : "";
  const message = record && typeof record === "object" && "message" in record && typeof record.message === "string" ? record.message.trim().toLowerCase() : "";
  if (["mailboxnotenabledforrestapi", "errormailboxnotenabledforrestapi"].includes(code) ||
    message.includes("resource could not be discovered") || message.includes("mailbox") && ["not enabled", "not licensed", "not found", "inactive", "soft-deleted"].some((part) => message.includes(part))) return new MailError("mail_provider_mailbox_unavailable");
  return new MailError(status === 401 || status === 403 ? "mail_provider_authorization_failed" : status === 404 ? "mail_provider_item_not_found" : status === 429 ? "mail_provider_rate_limited" : "mail_provider_unavailable", status);
}
