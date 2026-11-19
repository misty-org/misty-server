import * as Y from "yjs";

/**
 * The plain text the API indexes for search, read straight from each kind's Yjs layout (see
 * docs/plans/space-artifacts-contract.md in the app). Only words people wrote go out: never a
 * formula, an id or an embed's reference.
 */

/** The API's ceiling for one item's search projection, in characters. */
export const MAX_PLAIN_TEXT = 100_000;

/** A deck's slot names in reading order; slots with other names follow. */
const DECK_SLOTS = [
  "kicker",
  "title",
  "subtitle",
  "body",
  "left",
  "right",
  "visual",
  "caption",
  "number",
  "label",
  "quote",
  "attribution",
];

/** Collects text up to the ceiling, one part per line. */
class TextCollector {
  private readonly parts: string[] = [];
  private length = 0;

  add(value: unknown): void {
    if (typeof value !== "string") return;
    const text = value.replace(/\s+/gu, " ").trim();
    if (!text || this.full()) return;
    this.parts.push(text);
    this.length += text.length + 1;
  }

  full(): boolean {
    return this.length >= MAX_PLAIN_TEXT;
  }

  toString(): string {
    return this.parts.join("\n").slice(0, MAX_PLAIN_TEXT);
  }
}

/** The words of a Tiptap fragment: each block on its own line, marks dropped. */
function fragmentText(fragment: Y.XmlFragment | Y.XmlElement, out: TextCollector): void {
  let line = "";
  const flush = () => {
    out.add(line);
    line = "";
  };
  for (const child of fragment.toArray()) {
    if (child instanceof Y.XmlText) {
      for (const op of child.toDelta() as { insert?: unknown }[]) {
        if (typeof op.insert === "string") line += op.insert;
      }
    } else if (child instanceof Y.XmlElement) {
      flush();
      fragmentText(child, out);
    }
    if (out.full()) break;
  }
  flush();
}

function metaTitle(document: Y.Doc, name: string): string {
  const title = document.getMap<unknown>(name).get("title");
  return typeof title === "string" ? title.trim() : "";
}

export function deckTitle(document: Y.Doc): string {
  return metaTitle(document, "deck:meta");
}

export function workbookTitle(document: Y.Doc): string {
  return metaTitle(document, "wb:meta");
}

function deckSlides(document: Y.Doc): Y.Map<unknown>[] {
  const slides = document.getMap<unknown>("deck:slides");
  const ordered: Y.Map<unknown>[] = [];
  for (const id of document.getArray<unknown>("deck:order").toArray()) {
    const slide = typeof id === "string" ? slides.get(id) : undefined;
    if (slide instanceof Y.Map) ordered.push(slide);
  }
  return ordered;
}

/** The slides a deck shows in its order: what deck_slides counts. */
export function deckSlideCount(document: Y.Doc): number {
  return deckSlides(document).length;
}

/** A deck's title, then each slide's text slots and speaker notes, in slide order. */
export function deckPlainText(document: Y.Doc): string {
  const out = new TextCollector();
  out.add(deckTitle(document));
  for (const slide of deckSlides(document)) {
    const slots = slide.get("slots");
    if (slots instanceof Y.Map) {
      const names = [
        ...DECK_SLOTS.filter((name) => slots.has(name)),
        ...Array.from(slots.keys()).filter((name) => !DECK_SLOTS.includes(name)).sort(),
      ];
      for (const name of names) {
        const slot = slots.get(name);
        if (slot instanceof Y.XmlFragment) fragmentText(slot, out);
        // An embed's own title is the label the slide shows for it.
        else if (slot instanceof Y.Map) out.add(slot.get("title"));
      }
    }
    const notes = slide.get("notes");
    if (notes instanceof Y.Text) out.add(notes.toString());
    if (out.full()) break;
  }
  return out.toString();
}

function workbookSheets(document: Y.Doc): Y.Map<unknown>[] {
  const sheets = document.getMap<unknown>("wb:sheets");
  const ordered: Y.Map<unknown>[] = [];
  for (const id of document.getArray<unknown>("wb:sheetOrder").toArray()) {
    const sheet = typeof id === "string" ? sheets.get(id) : undefined;
    if (sheet instanceof Y.Map) ordered.push(sheet);
  }
  return ordered;
}

function cellValue(cell: unknown): string {
  if (cell instanceof Y.Map) cell = cell.toJSON();
  if (typeof cell !== "object" || cell === null) return "";
  const value = (cell as { v?: unknown }).v;
  return typeof value === "string" ? value : "";
}

/** The filled cells across a workbook's sheets: what workbook_cells counts. */
export function workbookCellCount(document: Y.Doc): number {
  let count = 0;
  for (const sheet of workbookSheets(document)) {
    const cells = sheet.get("cells");
    if (!(cells instanceof Y.Map)) continue;
    for (const cell of cells.values()) if (cellValue(cell) !== "") count += 1;
  }
  return count;
}

function orderIndex(order: unknown): Map<string, number> {
  const index = new Map<string, number>();
  if (order instanceof Y.Array) {
    order.toArray().forEach((id, position) => {
      if (typeof id === "string") index.set(id, position);
    });
  }
  return index;
}

/**
 * A workbook's title, then each sheet's name and its typed values in row then column order.
 * Formulas (values starting with "=") are left out: their text is references, not words.
 */
export function workbookPlainText(document: Y.Doc): string {
  const out = new TextCollector();
  out.add(workbookTitle(document));
  for (const sheet of workbookSheets(document)) {
    out.add(sheet.get("name"));
    const cells = sheet.get("cells");
    if (!(cells instanceof Y.Map)) continue;
    const rows = orderIndex(sheet.get("rows"));
    const cols = orderIndex(sheet.get("cols"));
    const values: { row: number; col: number; text: string }[] = [];
    for (const [key, cell] of cells.entries()) {
      const text = cellValue(cell);
      if (!text || text.startsWith("=")) continue;
      const [rowID = "", colID = ""] = key.split(":");
      values.push({
        row: rows.get(rowID) ?? Number.MAX_SAFE_INTEGER,
        col: cols.get(colID) ?? Number.MAX_SAFE_INTEGER,
        text,
      });
    }
    values.sort((left, right) => left.row - right.row || left.col - right.col);
    for (const value of values) {
      out.add(value.text);
      if (out.full()) break;
    }
    if (out.full()) break;
  }
  return out.toString();
}

/** A whiteboard's words: the text of its text elements, and its frames' names. */
export function drawingPlainText(document: Y.Doc): string {
  const out = new TextCollector();
  const elements = Array.from(document.getMap<unknown>("drawing:elements").values())
    .filter(
      (element): element is Record<string, unknown> =>
        typeof element === "object" && element !== null && (element as { isDeleted?: unknown }).isDeleted !== true,
    )
    .sort(
      (left, right) =>
        String(left.index ?? "").localeCompare(String(right.index ?? "")) ||
        String(left.id).localeCompare(String(right.id)),
    );
  for (const element of elements) {
    if (element.type === "text") out.add(element.text);
    else if (element.type === "frame" || element.type === "magicframe") out.add(element.name);
    if (out.full()) break;
  }
  return out.toString();
}
