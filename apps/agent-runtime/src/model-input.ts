import type { FilePart, ImagePart, ModelMessage, TextPart } from "ai";
import { companionImageParts } from "./companion-images.js";
import type { SpaceTaskContext } from "./types.js";

/** The first user turn: the request plus its supplied files and screen captures. */
export function initialMessages(context: SpaceTaskContext): ModelMessage[] {
  const files = [...(context.attachments ?? []), ...(context.capture ? [context.capture] : [])];
  if (!files.length && !context.display_captures?.length) return [{ role: "user", content: context.prompt }];
  const manifest = (context.attachments ?? [])
    .map((file) => JSON.stringify({ attachmentId: file.id, name: file.name, mimeType: file.mime_type }))
    .join("\n");
  return [{
    role: "user",
    content: [
      { type: "text", text: `${context.prompt}\nSupplied task files (use these IDs for upload):\n${manifest}` },
      ...companionImageParts(context.display_captures),
      ...files.flatMap<TextPart | ImagePart | FilePart>((file) =>
        file.mime_type === "text/plain"
          ? { type: "text" as const, text: `Attachment ${file.name}:\n${Buffer.from(file.data_url.split(",")[1] ?? "", "base64").toString("utf8")}` }
          : file.mime_type === "application/pdf"
            ? { type: "file" as const, data: file.data_url, mediaType: "application/pdf", filename: file.name }
            : [
                { type: "text" as const, text: `${file.name} (${file.width} x ${file.height} screenshot pixels; top-left origin)` },
                { type: "image" as const, image: file.data_url, mediaType: file.mime_type },
              ],
      ),
    ],
  }];
}
