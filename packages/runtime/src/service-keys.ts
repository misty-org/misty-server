import { readFile } from "node:fs/promises";
import { importSPKI } from "jose";
import { z } from "zod";

const keysSchema = z.array(z.object({ id: z.string().regex(/^[a-zA-Z0-9_-]{1,100}$/), publicKey: z.string().min(1).max(4096) }).strict()).min(1).max(5);
export async function loadServicePublicKeys(path: string) {
  const raw = await readFile(path, "utf8");
  if (Buffer.byteLength(raw) > 32768) throw new Error("Service verification key file exceeds size limit");
  const definitions = keysSchema.parse(JSON.parse(raw));
  if (new Set(definitions.map((definition) => definition.id)).size !== definitions.length) throw new Error("Duplicate service signing key IDs");
  const pairs = await Promise.all(definitions.map(async (definition) => [definition.id, await importSPKI(definition.publicKey, "EdDSA")] as const));
  return new Map(pairs);
}
