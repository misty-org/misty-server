import { describe, expect, it } from "vitest";
import { deadlineNote } from "../src/turn-deadline.js";

describe("deadlineNote", () => {
  it("asks for the answer two turns before the limit, once", () => {
    expect(deadlineNote(10, 20, [])).toBeUndefined();
    const note = deadlineNote(18, 20, []);
    expect(note).toContain("2 model turns left");
    expect(deadlineNote(19, 20, [{ role: "user", content: note! }])).toBeUndefined();
  });

  it("leaves very short runs alone", () => {
    expect(deadlineNote(3, 4, [])).toBeUndefined();
  });
});
