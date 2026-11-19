import * as encoding from "lib0/encoding";
import { describe, expect, it } from "vitest";
import * as Y from "yjs";

import {
  MAX_PLAIN_TEXT,
  deckPlainText,
  deckSlideCount,
  deckTitle,
  drawingPlainText,
  workbookCellCount,
  workbookPlainText,
} from "../src/item-text";
import { gateDocumentUpdate } from "../src/room-message-gate";

function paragraph(text: string, bold?: string): Y.XmlElement {
  const element = new Y.XmlElement("paragraph");
  const run = new Y.XmlText();
  run.insert(0, text);
  // A run not yet in a document reports no length, so the bold part goes in by the text's own.
  if (bold) run.insert(text.length, bold, { bold: true });
  element.insert(0, [run]);
  return element;
}

function addSlide(doc: Y.Doc, id: string, slots: Record<string, Y.XmlFragment | Y.Map<unknown>>, notes = ""): void {
  const slide = new Y.Map<unknown>();
  slide.set("layout", "bullets");
  slide.set("hidden", false);
  const slotMap = new Y.Map<unknown>();
  for (const [name, value] of Object.entries(slots)) slotMap.set(name, value);
  slide.set("slots", slotMap);
  const text = new Y.Text();
  text.insert(0, notes);
  slide.set("notes", text);
  doc.getMap<unknown>("deck:slides").set(id, slide);
  doc.getArray<string>("deck:order").push([id]);
}

function fragment(...paragraphs: Y.XmlElement[]): Y.XmlFragment {
  const value = new Y.XmlFragment();
  value.insert(0, paragraphs);
  return value;
}

function syncUpdateMessage(update: Uint8Array): ArrayBuffer {
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, 0);
  encoding.writeVarUint(encoder, 2);
  encoding.writeVarUint8Array(encoder, update);
  const bytes = encoding.toUint8Array(encoder);
  return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
}

describe("deck projection", () => {
  it("reads the title, every text slot in reading order and the notes", () => {
    const doc = new Y.Doc();
    doc.getMap<unknown>("deck:meta").set("title", "Launch plan");
    addSlide(doc, "s1", { body: fragment(paragraph("Ship in ", "March")), title: fragment(paragraph("Timeline")) }, "Say it slowly");
    const chart = new Y.Map<unknown>();
    chart.set("type", "data-chart");
    chart.set("item_id", "workbook_1");
    chart.set("part_id", "chart_1");
    chart.set("title", "Revenue by month");
    addSlide(doc, "s2", { visual: chart, caption: fragment(paragraph("Q1 numbers")) });

    expect(deckTitle(doc)).toBe("Launch plan");
    expect(deckSlideCount(doc)).toBe(2);
    expect(deckPlainText(doc).split("\n")).toEqual([
      "Launch plan",
      "Timeline",
      "Ship in March",
      "Say it slowly",
      "Revenue by month",
      "Q1 numbers",
    ]);
  });

  it("counts only ordered slides that exist", () => {
    const doc = new Y.Doc();
    addSlide(doc, "s1", {});
    doc.getArray<string>("deck:order").push(["missing"]);
    expect(deckSlideCount(doc)).toBe(1);
  });
});

describe("workbook projection", () => {
  function sheet(doc: Y.Doc, id: string, name: string, cells: Record<string, string>, rows: string[], cols: string[]) {
    const value = new Y.Map<unknown>();
    value.set("name", name);
    const rowOrder = new Y.Array<string>();
    rowOrder.push(rows);
    const colOrder = new Y.Array<string>();
    colOrder.push(cols);
    value.set("rows", rowOrder);
    value.set("cols", colOrder);
    const cellMap = new Y.Map<unknown>();
    for (const [key, v] of Object.entries(cells)) cellMap.set(key, { v });
    value.set("cells", cellMap);
    doc.getMap<unknown>("wb:sheets").set(id, value);
    doc.getArray<string>("wb:sheetOrder").push([id]);
  }

  it("reads sheet names and typed values in row and column order, never formulas", () => {
    const doc = new Y.Doc();
    doc.getMap<unknown>("wb:meta").set("title", "Budget");
    sheet(
      doc,
      "sh1",
      "Costs",
      { "r2:c1": "Rent", "r1:c2": "Amount", "r1:c1": "Item", "r2:c2": "1200", "r3:c2": "=SUM({r:r2,c:c2})", "r3:c1": "" },
      ["r1", "r2", "r3"],
      ["c1", "c2"],
    );
    expect(workbookPlainText(doc).split("\n")).toEqual(["Budget", "Costs", "Item", "Amount", "Rent", "1200"]);
    // A formula is a filled cell; an empty one isn't.
    expect(workbookCellCount(doc)).toBe(5);
  });

  it("stops at the search ceiling", () => {
    const doc = new Y.Doc();
    const cells: Record<string, string> = {};
    const rows: string[] = [];
    for (let row = 0; row < 3000; row += 1) {
      rows.push(`r${row}`);
      cells[`r${row}:c`] = "x".repeat(50);
    }
    sheet(doc, "sh1", "Big", cells, rows, ["c"]);
    expect(workbookPlainText(doc).length).toBeLessThanOrEqual(MAX_PLAIN_TEXT);
  });
});

describe("drawing projection", () => {
  it("reads live text elements and frame names in scene order", () => {
    const doc = new Y.Doc();
    const elements = doc.getMap<unknown>("drawing:elements");
    elements.set("b", { id: "b", type: "text", text: "Checkout", index: "a1" });
    elements.set("a", { id: "a", type: "frame", name: "Payments", index: "a0" });
    elements.set("c", { id: "c", type: "text", text: "Gone", index: "a2", isDeleted: true });
    elements.set("d", { id: "d", type: "rectangle", index: "a3" });
    expect(drawingPlainText(doc)).toBe("Payments\nCheckout");
  });
});

describe("growth limits", () => {
  it("refuses an update that adds a slide past the limit but lets an over-limit deck shrink", () => {
    const room = new Y.Doc();
    addSlide(room, "s1", {});
    addSlide(room, "s2", {});
    const limit = { limit: 2, count: deckSlideCount, code: "slide_limit_reached" };

    const client = new Y.Doc();
    Y.applyUpdate(client, Y.encodeStateAsUpdate(room));
    const before = Y.encodeStateVector(client);
    addSlide(client, "s3", {});
    const growing = syncUpdateMessage(Y.encodeStateAsUpdate(client, before));
    expect(gateDocumentUpdate(room, growing, limit)).toMatchObject({ status: "blocked", code: "slide_limit_reached" });

    const shrinker = new Y.Doc();
    Y.applyUpdate(shrinker, Y.encodeStateAsUpdate(room));
    const shrinkBase = Y.encodeStateVector(shrinker);
    shrinker.getArray<string>("deck:order").delete(1, 1);
    const shrinking = syncUpdateMessage(Y.encodeStateAsUpdate(shrinker, shrinkBase));
    expect(gateDocumentUpdate(room, shrinking, { ...limit, limit: 1 })).toBeNull();
    expect(gateDocumentUpdate(room, growing)).toBeNull();
  });
});
