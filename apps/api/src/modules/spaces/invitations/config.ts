import { createHmac } from "node:crypto";
import { readFile } from "node:fs/promises";
import { z } from "zod";
export type InvitationKeys = { active: string; keys: ReadonlyMap<string, Buffer> };
export type InvitationConfig = { baseUrl: string; keys: InvitationKeys };
export async function loadInvitationConfig(env: NodeJS.ProcessEnv): Promise<InvitationConfig | null> {
  const file = env.MISTY_INVITATION_TOKEN_KEYS_FILE?.trim();
  if (!file) return null;
  const bytes = await readFile(file); if (bytes.byteLength > 16384) throw new Error("Invitation token key file is too large");
  const document = z.object({ active: z.string().min(1).max(100), keys: z.array(z.object({ id: z.string().min(1).max(100), key: z.string() }).strict()).min(1).max(10) }).strict().parse(JSON.parse(bytes.toString("utf8")));
  const keys = new Map<string, Buffer>();
  for (const entry of document.keys) {
    const key = Buffer.from(entry.key, "base64");
    if (key.byteLength !== 32 || key.toString("base64") !== entry.key || keys.has(entry.id)) throw new Error("Invalid invitation token key"); keys.set(entry.id, key);
  }
  if (!keys.has(document.active)) throw new Error("Active invitation token key is missing");
  const base = new URL(env.MISTY_INVITATION_URL_BASE?.trim() || "https://mistysys.com/invite");
  const local = env.MISTY_DEPLOYMENT_MODE === "self_hosted" && ["localhost", "127.0.0.1", "[::1]"].includes(base.hostname);
  if (!(base.protocol === "https:" || base.protocol === "http:" && local) || base.username || base.password || base.search || base.hash) throw new Error("Invalid Space invitation base URL");
  return { baseUrl: base.href.replace(/\/$/, ""), keys: { active: document.active, keys } };
}
export function invitationToken(keys: InvitationKeys, keyId: string, id: string, generation: string, email: string) {
  const key = keys.keys.get(keyId); if (!key) throw new Error("Invitation signing key is unavailable");
  return createHmac("sha256", key).update(JSON.stringify(["misty-space-invitation-v1", id, generation, email])).digest("base64url");
}
