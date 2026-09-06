import { z } from "zod";
import { catalogDocument } from "./catalog.generated.js";

const platform = z.object({ runtime: z.enum(["downloaded", "hosted", "embedded", "unsupported"]), entry: z.string().optional(),
  sha256: z.string().optional(), style_sha256: z.string().optional(), signature: z.string().optional(), signature_key_id: z.string().optional(),
  download_bytes: z.number().int().nonnegative().optional(), additional_storage_bytes: z.number().int().nonnegative().optional() });
const appSchema = z.object({ id: z.string().min(1).max(80), app_id: z.string().optional(), slug: z.string().optional(),
  name: z.string(), publisher: z.string(), description: z.string(), version: z.string().min(1).max(40), permission_version: z.number().int().positive(),
  minimum_host_protocol: z.number().int().positive(), official: z.boolean(), age_rating: z.string(), scopes: z.array(z.string()), desktop: platform, mobile: platform });
export type OfficialApp = z.infer<typeof appSchema>;
export function createOfficialCatalog(document: unknown = catalogDocument) {
  const parsed = z.object({ schema_version: z.literal(1), host_protocol_version: z.number().int().positive(), apps: z.array(appSchema) }).parse(document);
  if (new Set(parsed.apps.map((app) => app.id)).size !== parsed.apps.length) throw new Error("Duplicate official app IDs");
  return { hostProtocol: parsed.host_protocol_version, all: () => structuredClone(parsed.apps),
    find: (id: string) => { const app = parsed.apps.find((app) => app.id === id.trim().toLowerCase()); return app ? structuredClone(app) : null; } };
}
