import { z } from "zod";
import { MailError, providerMailError } from "../errors.js";
import type { MailWriteGuard } from "./actions.js";

const sessionSchema = z.object({ uploadUrl: z.string().max(16384), expirationDateTime: z.string().max(128), nextExpectedRanges: z.array(z.string().max(64)).length(1) });
const chunkSize = 2 * 1024 * 1024;

/** Outlook attachment sessions use a preauthenticated URL, never our OAuth token. */
export async function uploadGraphAttachment(raw: unknown, bytes: Buffer, signal: AbortSignal, guard: MailWriteGuard, fetcher: typeof fetch = fetch, now = Date.now) {
  const parsed = sessionSchema.safeParse(raw);
  if (!parsed.success || parsed.data.nextExpectedRanges[0] !== "0-" || !bytes.length || bytes.length > 10 * 1024 * 1024) throw new MailError("mail_provider_unavailable");
  const session = parsed.data, expiresAt = Date.parse(session.expirationDateTime); let url: URL;
  try { url = new URL(session.uploadUrl); } catch { throw new MailError("mail_provider_unavailable"); }
  if (session.uploadUrl !== session.uploadUrl.trim() || url.origin !== "https://outlook.office.com" || url.username || url.password || url.hash ||
    !/^\/api\/v2\.0\/Users\('[^/]*'\)\/Messages\('[^/]*'\)\/AttachmentSessions\('[^/]*'\)$/i.test(url.pathname) || !url.searchParams.get("authtoken") || !Number.isFinite(expiresAt)) throw new MailError("mail_provider_unavailable");
  for (let start = 0; start < bytes.length; start += chunkSize) {
    const end = Math.min(start + chunkSize, bytes.length), chunk = bytes.subarray(start, end);
    await guard(async () => {
      if (expiresAt <= now()) throw new MailError("mail_provider_unavailable");
      const current = AbortSignal.any([signal, AbortSignal.timeout(15000)]);
      try {
        current.throwIfAborted();
        const response = await fetcher(session.uploadUrl, { method: "PUT", redirect: "error", signal: current, body: new Uint8Array(chunk),
          headers: { "Content-Type": "application/octet-stream", "Content-Length": String(chunk.length), "Content-Range": `bytes ${start}-${end - 1}/${bytes.length}` } });
        const reader = response.body?.getReader(), parts: Uint8Array[] = []; let total = 0;
        if (reader) try {
          for (;;) {
            const next = await reader.read(); if (next.done) break;
            total += next.value.length; if (total > 256 * 1024) throw new MailError("mail_response_too_large");
            parts.push(next.value);
          }
        } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
        if (!response.ok) {
          // Failure of this short-lived upload URL does not imply OAuth consent
          // was revoked. Never leak its URL or response into errors or logs.
          if (response.status === 401 || response.status === 403) throw new MailError("mail_provider_unavailable");
          throw providerMailError(response.status, null);
        }
        if (end === bytes.length) { if (response.status !== 201) throw new MailError("mail_provider_unavailable"); return; }
        if (response.status !== 200) throw new MailError("mail_provider_unavailable");
        let next: unknown;
        try { next = JSON.parse(Buffer.concat(parts, total).toString("utf8")); } catch { throw new MailError("mail_provider_unavailable"); }
        const ranges = z.object({ nextExpectedRanges: z.array(z.string()).length(1) }).safeParse(next);
        if (!ranges.success || ![String(end), `${end}-`].includes(ranges.data.nextExpectedRanges[0]!)) throw new MailError("mail_provider_unavailable");
      } catch (error) { if (error instanceof MailError) throw error; throw new MailError("mail_provider_unavailable"); }
    });
  }
}
