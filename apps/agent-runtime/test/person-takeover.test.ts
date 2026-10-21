import { describe, expect, it } from "vitest";
import { afterTakeover, resumeNote } from "../src/person-takeover.js";

describe("take over", () => {
  it("resumes with the person's messages, then a note to read the window again", () => {
    const next = afterTakeover([{ role: "user", content: "Compare the plans" }], ["Use the yearly price"]);
    expect(next.map((message) => message.content)).toEqual(["Compare the plans", "Use the yearly price", resumeNote]);
    expect(resumeNote).toContain("read your tabs again");
  });
});
