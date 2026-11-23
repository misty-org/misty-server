import { jsonResponse } from "./control_protocol";
import { writeMarkdownContent } from "./document_content";
import type { RoomControlContext } from "./room_control";

const BOOTSTRAP_APPLIED_KEY = "bootstrap:applied";

export async function bootstrapNote(
  context: RoomControlContext,
  payload: Record<string, unknown>,
): Promise<Response> {
  if (!context.supportsMarkdownBootstrap) {
    return jsonResponse({ code: "unknown_command" }, 400);
  }
  const title =
    typeof payload.title === "string" ? payload.title.trim() : "";
  const markdown =
    typeof payload.markdown === "string" ? payload.markdown.trim() : "";
  if (
    !title ||
    title.length > 500 ||
    !markdown ||
    markdown.length > 100_000
  ) {
    return jsonResponse({ code: "invalid_bootstrap" }, 400);
  }
  if (
    (await context.storage.get<boolean>(BOOTSTRAP_APPLIED_KEY)) === true
  ) {
    await context.publishNoteProjection();
    return jsonResponse({ ok: true, initialized: false });
  }

  const initialized = context.document.share.size === 0;
  if (initialized) {
    writeMarkdownContent(context.document, title, markdown, 1);
    await context.save();
  }
  await context.storage.put(BOOTSTRAP_APPLIED_KEY, true);
  return jsonResponse({ ok: true, initialized });
}

export async function replaceNoteMarkdown(
  context: RoomControlContext,
  payload: Record<string, unknown>,
): Promise<Response> {
  if (!context.supportsMarkdownBootstrap) {
    return jsonResponse({ code: "unknown_command" }, 400);
  }
  const title = typeof payload.title === "string" ? payload.title.trim() : "";
  const markdown = typeof payload.markdown === "string" ? payload.markdown.trim() : "";
  if (!title || title.length > 500 || markdown.length > 100_000) {
    return jsonResponse({ code: "invalid_note_content" }, 400);
  }
  const metadata = context.document.getMap<unknown>("misty:document");
  const revision =
    Math.max(
      (await context.storage.get<number>("pendingVersion")) ?? 0,
      Number(metadata.get("pending_version") ?? 0),
      Number(metadata.get("applied_version") ?? 0),
    ) + 1;
  writeMarkdownContent(context.document, title, markdown, revision);
  await context.storage.put("pendingVersion", revision);
  await context.save();
  return jsonResponse({ ok: true });
}
