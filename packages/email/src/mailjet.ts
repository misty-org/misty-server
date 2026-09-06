import { z } from "zod";
import type { Environment } from "../../runtime/src/config.js";

export class EmailDeliveryError extends Error {
  constructor(readonly reason: "unconfigured" | "transport" | "response" | "busy") { super("Email delivery failed"); }
}
export function loadMailjetConfig(env: Environment) {
  const apiKey = env.MAILJET_API_KEY?.trim(), secretKey = env.MAILJET_SECRET_KEY?.trim(), fromEmail = env.MAILJET_FROM_EMAIL?.trim();
  if (!apiKey && !secretKey && !fromEmail) return null;
  if (!apiKey || !secretKey || !fromEmail || !z.email().safeParse(fromEmail).success) throw new Error("Valid MAILJET_API_KEY, MAILJET_SECRET_KEY and MAILJET_FROM_EMAIL are required together");
  const base = new URL(env.MAILJET_API_BASE_URL?.trim() || "https://api.mailjet.com");
  if (base.protocol !== "https:" || base.username || base.password || base.search || base.hash || base.pathname !== "/") throw new Error("MAILJET_API_BASE_URL must be an HTTPS origin");
  return { apiKey, secretKey, fromEmail, fromName: env.MAILJET_FROM_NAME?.trim() ?? "", endpoint: `${base.origin}/v3.1/send` };
}
export type EmailMessage = { to: string; subject: string; text: string; html: string };
export type EmailSender = (message: EmailMessage) => Promise<void>;
const resultSchema = z.object({ Messages: z.array(z.object({ Status: z.string() })).length(1) });

/** One bounded provider call; ambiguous outcomes are not retried blindly. */
export function createMailjetSender(config: ReturnType<typeof loadMailjetConfig>, request: typeof fetch = fetch): EmailSender {
  let active = 0;
  return async (message) => {
    if (!config) throw new EmailDeliveryError("unconfigured");
    if (active >= 16) throw new EmailDeliveryError("busy");
    active++;
    try {
      const response = await request(config.endpoint, { method: "POST", redirect: "error", signal: AbortSignal.timeout(10000),
        headers: { Authorization: `Basic ${Buffer.from(`${config.apiKey}:${config.secretKey}`).toString("base64")}`, "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ Messages: [{ From: { Email: config.fromEmail, Name: config.fromName }, To: [{ Email: message.to }],
          Subject: message.subject, TextPart: message.text, HTMLPart: message.html }] }),
      });
      if (!response.ok) { await response.body?.cancel(); throw new EmailDeliveryError("response"); }
      const reader = response.body?.getReader();
      if (!reader) throw new EmailDeliveryError("response");
      const chunks: Uint8Array[] = [];
      let bytes = 0;
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          bytes += value.byteLength;
          if (bytes > 16384) { await reader.cancel(); throw new EmailDeliveryError("response"); }
          chunks.push(value);
        }
      } finally { reader.releaseLock(); }
      let parsed: unknown;
      try { parsed = JSON.parse(Buffer.concat(chunks).toString("utf8")); } catch { throw new EmailDeliveryError("response"); }
      const result = resultSchema.safeParse(parsed);
      if (!result.success || result.data.Messages[0]?.Status.toLowerCase() !== "success") throw new EmailDeliveryError("response");
    } catch (error) {
      // Provider errors can echo addresses, credentials or reset links. Never
      // retain their response body, exception message or cause in application logs.
      throw error instanceof EmailDeliveryError ? error : new EmailDeliveryError("transport");
    } finally { active--; }
  };
}
