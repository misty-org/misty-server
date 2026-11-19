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
  protected override readonly supportsDrawingScene = true;
}

/**
 * A Space library image being edited together: the edit (crop, light, color, size) and its
 * marks, as data. The pixels stay in the library; the room only holds what is done to them
 * until someone saves.
 */
export class ImageRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "image" as const;
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
 * A deck (Slides): slides built from layouts and a theme, their text slots, embeds and speaker
 * notes (deck:meta, deck:order, deck:slides). Each save projects its text and slide count to the
 * API, and the Space level's deck_slides limit refuses more slides.
 */
export class DeckRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "deck" as const;
}

/**
 * A workbook (Data): sheets of id-anchored cells, charts and a dashboard (wb:meta,
 * wb:sheetOrder, wb:sheets, wb:charts, wb:dashboard). Each save projects its sheet names and
 * typed values and its filled-cell count, and the level's workbook_cells limit refuses more.
 */
export class WorkbookRoom extends PersistentDocumentRoom {
  protected readonly resourceType = "workbook" as const;
}

export type { Env } from "./document-room";
