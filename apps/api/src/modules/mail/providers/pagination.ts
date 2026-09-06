import { MailError } from "../errors.js";
export function graphNextToken(nextLink: string): string {
  if (!nextLink.trim()) return "";
  if (nextLink.length > 8192) throw new MailError("mail_provider_unavailable");
  let next: URL;
  try { next = new URL(nextLink, "https://graph.microsoft.com/v1.0/"); } catch { throw new MailError("mail_provider_unavailable"); }
  const token = next.searchParams.get("$skiptoken");
  if (next.origin !== "https://graph.microsoft.com" || next.username || next.password || next.hash || !token || token.length > 4096) throw new MailError("mail_provider_unavailable");
  return token;
}
