import { createHmac } from "node:crypto";
import { readFile } from "node:fs/promises";
import { z } from "zod";

const schema = z.object({ active: z.string().min(1).max(100), keys: z.array(z.object({ id: z.string().min(1).max(100), key: z.string() }).strict()).min(1).max(10) }).strict();
export type RecoveryTokenKeys = { active: string; keys: ReadonlyMap<string, Buffer> };
export async function loadRecoveryTokenKeys(file: string): Promise<RecoveryTokenKeys> {
  const bytes = await readFile(file);
  if (bytes.byteLength > 16384) throw new Error("Recovery token key file is too large");
  const document = schema.parse(JSON.parse(bytes.toString("utf8")));
  const keys = new Map<string, Buffer>();
  for (const entry of document.keys) {
    const key = Buffer.from(entry.key, "base64");
    if (key.length !== 32 || key.toString("base64") !== entry.key || keys.has(entry.id)) throw new Error("Invalid recovery token key");
    keys.set(entry.id, key);
  }
  if (!keys.has(document.active)) throw new Error("Active recovery token key is missing");
  return { active: document.active, keys };
}
/** Stable retry token without storing a redeemable credential in the job table. */
export function recoveryToken(keys: RecoveryTokenKeys, keyId: string, jobId: string, email: string) {
  const key = keys.keys.get(keyId);
  if (!key) throw new Error("Recovery token key unavailable");
  return createHmac("sha256", key).update(JSON.stringify(["misty-password-recovery-v1", jobId, email])).digest("base64url");
}
