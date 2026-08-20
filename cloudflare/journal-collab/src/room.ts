import { PersistentDocumentRoom } from "./document-room";

/** Collaborative TipTap document. */
export class NoteRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "note" as const;
  protected override readonly supportsMarkdownBootstrap = true;
  protected override readonly supportsNoteProjection = true;
}

/** Collaborative Excalidraw scene. */
export class DrawingRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "drawing" as const;
}

export type { Env } from "./document-room";
