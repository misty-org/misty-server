import { createHmac, timingSafeEqual } from "node:crypto";
import type { Pool } from "pg";
import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { journalEvent } from "../journal/access.js";
import type { CollaborationConfig } from "./config.js";

const text = (maximum: number) => z.string().refine((value) => [...value].length <= maximum);
const projection = z.object({ note_id: z.string().trim().min(1), revision: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
  title: z.string().trim().min(1).refine((value) => [...value].length <= 500), markdown: text(100000).default(""), plain_text: text(100000).default(""),
  outgoing_note_ids: z.array(z.string()).max(500).nullable().optional().transform((value) => [...new Set((value ?? []).map((id) => id.trim()).filter(Boolean))]) });
export function signCollaborationPayload(secret: Buffer, timestamp: string, body: Uint8Array) {
  return createHmac("sha256", secret).update(timestamp).update("\n").update(body).digest("base64url");
}
export function createNoteProjectionRepository(pool: Pool) {
  return async (input: z.output<typeof projection>) => withTransaction(pool, async (tx) => {
    const initial = (await tx.query<{ space_id: string }>("SELECT space_id FROM space_notes WHERE id=$1", [input.note_id])).rows[0];
    if (!initial || !(await tx.query("SELECT id FROM spaces WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [initial.space_id])).rowCount) return false;
    const row = (await tx.query<{ creator_user_id: string }>(`UPDATE space_notes SET title_projection=$2,markdown_projection=$3,plain_text_projection=$4,
      collaboration_revision=$5,updated_at=now() WHERE id=$1 AND space_id=$6 AND lifecycle_state='active' AND collaboration_revision<$5 RETURNING creator_user_id`,
    [input.note_id, input.title, input.markdown, input.plain_text, input.revision, initial.space_id])).rows[0];
    if (!row) return false;
    await tx.query("DELETE FROM space_note_links WHERE source_note_id=$1", [input.note_id]);
    await tx.query(`INSERT INTO space_note_links(source_note_id,target_note_id) SELECT $1,id FROM space_notes
      WHERE id=ANY($2::text[]) AND id<>$1 AND space_id=$3 AND lifecycle_state='active' ON CONFLICT DO NOTHING`, [input.note_id, input.outgoing_note_ids, initial.space_id]);
    await journalEvent(tx, initial.space_id, row.creator_user_id, "note", "projection.updated", input.note_id);
    return true;
  }, { mode: "service" });
}
export function createNoteProjectionRoutes(options: { config: CollaborationConfig | null; apply: ReturnType<typeof createNoteProjectionRepository>; now?: () => number }) {
  const app = new Hono();
  app.use("/internal/journal/note-projections", bodyLimit({ maxSize: 512 * 1024, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
  app.post("/internal/journal/note-projections", async (c) => {
    c.header("Cache-Control", "no-store");
    if (!options.config) return c.json({ code: "collaboration_unavailable" }, 503);
    const body = new Uint8Array(await c.req.arrayBuffer());
    const timestamp = c.req.header("X-Misty-Timestamp") ?? "", signature = c.req.header("X-Misty-Signature") ?? "";
    const seconds = /^-?\d{1,13}$/.test(timestamp) ? Number(timestamp) : NaN;
    const valid = Number.isSafeInteger(seconds) && Math.abs((options.now?.() ?? Date.now()) - seconds * 1000) <= 300000 &&
      /^[A-Za-z0-9_-]{43}$/.test(signature) && [options.config.projectionSecret, options.config.previousProjectionSecret].some((secret) =>
        secret !== null && timingSafeEqual(Buffer.from(signCollaborationPayload(secret, timestamp, body)), Buffer.from(signature)));
    if (!valid) return c.json({ code: "unauthorized" }, 401);
    const input = projection.safeParse(await Promise.resolve().then(() => JSON.parse(new TextDecoder().decode(body))).catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    return c.json({ applied: await options.apply(input.data) });
  });
  return app;
}
