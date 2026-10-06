import { describe, expect, it } from "vitest";
import { visualToolOutput } from "../src/model-tools.js";

describe("visual tool output", () => {
  it("returns browser_act's final screenshot as an image beside its report", () => {
    const output = visualToolOutput("browser.act", {
      status: "done",
      summary: "Saved",
      cursor: { x: 0.5, y: 0.25 },
      image: { dataUrl: "data:image/png;base64,AAAA", width: 1, height: 1 },
    });
    expect(output.type).toBe("content");
    if (output.type !== "content") return;
    expect(output.value[0]).toMatchObject({ type: "text" });
    expect(JSON.parse((output.value[0] as { text: string }).text)).toMatchObject({ status: "done", cursor: { x: 0.5 } });
    expect(output.value[1]).toEqual({ type: "image-data", data: "AAAA", mediaType: "image/png" });
  });

  it("keeps other tools as text", () => {
    expect(visualToolOutput("notes.search", { image: { dataUrl: "data:image/png;base64,AAAA" } }).type).toBe("text");
  });
});
