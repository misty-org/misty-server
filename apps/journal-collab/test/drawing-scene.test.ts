import { describe, expect, it } from "vitest";
import * as Y from "yjs";

import {
  applyDrawingSceneMutation,
  buildDrawingSceneMutation,
  drawingSceneHash,
  drawingSceneState,
} from "../src/drawing-scene";

describe("drawing scene control", () => {
  it("normalizes native shapes and supports partial collaborative updates", async () => {
    const doc = new Y.Doc();
    const first = buildDrawingSceneMutation(doc, {
      elements: [
        { id: "box_1", type: "rectangle", x: 10, y: 20, width: 200, height: 100, backgroundColor: "#a5d8ff" },
        { id: "label_1", type: "text", x: 30, y: 50, text: "Misty", fontSize: 32 },
        { id: "arrow_1", type: "arrow", x: 210, y: 70, points: [[0, 0], [180, 40]] },
      ],
      scene: { viewBackgroundColor: "#ffffff" },
    }, 1_000);
    applyDrawingSceneMutation(doc, first);

    const before = drawingSceneState(doc, false) as { elements: Array<Record<string, unknown>>; scene: Record<string, unknown> };
    expect(before.elements).toHaveLength(3);
    expect(before.scene.viewBackgroundColor).toBe("#ffffff");
    expect(before.elements.find((element) => element.id === "label_1")).toMatchObject({
      type: "text", text: "Misty", originalText: "Misty", version: 1,
    });
    expect(before.elements.find((element) => element.id === "arrow_1")).toMatchObject({
      type: "arrow", endArrowhead: "arrow", version: 1,
    });

    applyDrawingSceneMutation(doc, buildDrawingSceneMutation(doc, {
      elements: [{ id: "box_1", x: 75 }],
      delete_element_ids: ["label_1"],
    }, 2_000));
    const after = drawingSceneState(doc, true) as { elements: Array<Record<string, unknown>> };
    expect(after.elements.find((element) => element.id === "box_1")).toMatchObject({ x: 75, version: 2 });
    expect(after.elements.find((element) => element.id === "label_1")).toMatchObject({ isDeleted: true, version: 2 });
    expect(await drawingSceneHash(doc)).toMatch(/^[a-f0-9]{64}$/u);
    doc.destroy();
  });

  it("replace mode tombstones elements omitted from the replacement", () => {
    const doc = new Y.Doc();
    applyDrawingSceneMutation(doc, buildDrawingSceneMutation(doc, {
      elements: [
        { id: "keep", type: "ellipse", x: 0, y: 0 },
        { id: "remove", type: "diamond", x: 100, y: 0 },
      ],
    }));
    const replacement = buildDrawingSceneMutation(doc, {
      mode: "replace",
      elements: [{ id: "keep", type: "ellipse", x: 40 }],
    });
    applyDrawingSceneMutation(doc, replacement);
    const state = drawingSceneState(doc, true) as { elements: Array<Record<string, unknown>> };
    expect(state.elements.find((element) => element.id === "keep")?.isDeleted).toBe(false);
    expect(state.elements.find((element) => element.id === "remove")?.isDeleted).toBe(true);
    doc.destroy();
  });

  it("rejects unsupported or malformed element records", () => {
    const doc = new Y.Doc();
    expect(() => buildDrawingSceneMutation(doc, { elements: [{ id: "bad", type: "selection" }] })).toThrow("invalid_drawing_element");
    expect(() => buildDrawingSceneMutation(doc, { elements: [{ id: "bad id", type: "rectangle" }] })).toThrow("invalid_drawing_element");
    doc.destroy();
  });
});
