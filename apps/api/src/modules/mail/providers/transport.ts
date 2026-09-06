import { MailError, providerMailError } from "../errors.js";
import type { ConnectionTokenLease } from "../../connections/token-broker.js";

export function createMailTransport(lease: ConnectionTokenLease, fetcher: typeof fetch = fetch) {
  const base = lease.account.provider === "google" ? "https://gmail.googleapis.com/gmail/v1/" : "https://graph.microsoft.com/v1.0/";
  let remainingBytes = 16 * 1024 * 1024;
  return async (parts: readonly string[], query: URLSearchParams, requestSignal: AbortSignal,
    write?: { method: "POST" | "PATCH" | "PUT" | "DELETE"; body?: unknown; discardResponse?: boolean }): Promise<unknown> => {
    if (parts.some((part) => part === "." || part === "..")) throw new MailError("mail_invalid_request");
    const url = new URL(parts.map(encodeURIComponent).join("/"), base); url.search = query.toString();
    const headers = new Headers({ Authorization: `Bearer ${lease.accessToken}`, Accept: "application/json" });
    const body = write?.body === undefined ? undefined : JSON.stringify(write.body);
    if (body !== undefined) {
      if (Buffer.byteLength(body) > 28 * 1024 * 1024) throw new MailError("mail_body_too_large");
      headers.set("Content-Type", "application/json");
    }
    if (lease.account.provider === "microsoft") headers.set("Prefer", 'IdType="ImmutableId"');
    const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(15000)]);
    try {
      signal.throwIfAborted();
      const response = await fetcher(url, { method: write?.method ?? "GET", ...(body === undefined ? {} : { body }), headers, signal, redirect: "error" });
      const reader = response.body?.getReader();
      if (!reader) { if (!response.ok) throw providerMailError(response.status, null); if (write?.discardResponse) return null; throw new MailError("mail_provider_unavailable"); }
      const chunks: Uint8Array[] = []; let bytes = 0;
      try {
        for (;;) {
          const next = await reader.read(); if (next.done) break;
          bytes += next.value.byteLength;
          remainingBytes -= next.value.byteLength;
          if (remainingBytes < 0) throw new MailError("mail_response_too_large");
          if (!response.ok || !write?.discardResponse) chunks.push(next.value);
        }
      } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
      if (response.ok && write?.discardResponse) return null;
      let data: unknown;
      try { data = JSON.parse(Buffer.concat(chunks, bytes).toString("utf8")); }
      catch { if (!response.ok) throw providerMailError(response.status, null); throw new MailError("mail_provider_unavailable"); }
      if (!response.ok) throw providerMailError(response.status, data);
      return data;
    } catch (error) { if (error instanceof MailError) throw error; throw new MailError("mail_provider_unavailable"); }
  };
}
