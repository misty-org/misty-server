import { PersistentDocumentRoom } from "./document-room";

/** Collaborative TipTap document. */
export class NoteRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "note" as const;
  protected override readonly supportsMarkdownBootstrap = true;
  protected override readonly supportsNoteProjection = true;
}

/**
 * A live code session: each file the members open is a shared text, and the steps they take are
 * a shared list. A plain authenticated Yjs room: every member edits, nothing is projected back
 * to the API, and ending the session purges it.
 */
export class CodeRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "code" as const;
}

/**
 * An app's live document (the SDK's `room()`): a plain authenticated Yjs room, as a code session's is.
 * Every member the app is for edits it, nothing is projected back to the API, and its id keeps it to its
 * Space and its app.
 */
export class AppRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "app" as const;
}

export type { Env } from "./document-room";
