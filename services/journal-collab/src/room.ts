import { PersistentDocumentRoom } from "./document_room";

export class NoteRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "note" as const;
  protected override readonly supportsMarkdownBootstrap = true;
  protected override readonly supportsNoteProjection = true;
}

export class CodeRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "code" as const;
}

export class AppRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "app" as const;
}

export { FileRoom } from "./room_file";

export type { Env } from "./document_room";
