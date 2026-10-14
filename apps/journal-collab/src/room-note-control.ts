import { jsonResponse } from "./control-protocol";
import { writeMarkdownContent } from "./document-content";
import type { RoomControlContext } from "./room-control";

const BOOTSTRAP_APPLIED_KEY = "bootstrap:applied";

/** Seeds a new note from markdown. Runs at most once per note. */
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
    // An older bootstrap may have persisted content before its resource
    // binding existed. Retry publication without replacing that content.
    await context.publishNoteProjection();
    return jsonResponse({ ok: true, initialized: false });
  }

  // A user may open and begin editing before this retryable command
  // arrives. Never replace a document that already has shared state.
  const initialized = context.document.share.size === 0;
  if (initialized) {
    writeMarkdownContent(context.document, title, markdown, 1);
    await context.save();
  }
  // Written after the document snapshot: a crash can cause one harmless
  // retry, but can never claim success before the content is durable.
  await context.storage.put(BOOTSTRAP_APPLIED_KEY, true);
  return jsonResponse({ ok: true, initialized });
}

/** Replaces a note's title and markdown, bumping its pending version. */
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
  const revision = ((await context.storage.get<number>("pendingVersion")) ?? 0) + 1;
  writeMarkdownContent(context.document, title, markdown, revision);
  await context.storage.put("pendingVersion", revision);
  await context.save();
  return jsonResponse({ ok: true });
}
