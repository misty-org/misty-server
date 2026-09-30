import type { FilePart, TextPart } from "ai";
import type { SpaceTaskContext } from "./types.js";

/** The label and binary image must describe the same screenshot coordinate space. */
export function companionImageParts(
  captures: SpaceTaskContext["display_captures"],
): Array<TextPart | FilePart> {
  return (captures ?? []).flatMap((capture) => [
    {
      type: "text" as const,
      text: `${capture.screen}${capture.primary ? " — primary focus (cursor)" : ""}: ${capture.width} x ${capture.height} screenshot pixels; top-left origin. Capture ${capture.id}; SHA-256 ${capture.content_hash}${capture.captured_at ? `; captured at ${new Date(capture.captured_at).toISOString()}` : ""}.`,
    },
    // Tagged inline data is the pinned SDK's current multimodal representation.
    // The API has already validated the data URL, MIME, decoded dimensions and hash.
    {
      type: "file" as const,
      data: {
        type: "data" as const,
        data: capture.data_url.slice(capture.data_url.indexOf(",") + 1),
      },
      mediaType: capture.mime_type,
    },
  ]);
}
