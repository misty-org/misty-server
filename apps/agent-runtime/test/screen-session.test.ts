import { describe, expect, it } from "vitest";
import { screenJoined } from "../src/screen-session.js";

describe("screenJoined", () => {
  it("sees a tab that joined the run, however Misty wrapped the result", () => {
    const opened = { status: "open", scopeId: "agent-scope-1", catalog_changed: true };
    expect(screenJoined([{ output: opened }])).toBe(true);
    expect(screenJoined([{ output: JSON.stringify(opened) }])).toBe(true);
    expect(screenJoined([{ output: { type: "json", value: { content: [{ text: JSON.stringify(opened) }] } } }])).toBe(true);
  });

  it("ignores other results and failed opens", () => {
    expect(screenJoined([{ output: { status: "screen_requested" } }])).toBe(false);
    expect(screenJoined([{ output: { status: "failure", catalog_changed: true } }])).toBe(false);
    expect(screenJoined([{ output: "plain text mentioning catalog_changed" }])).toBe(false);
    expect(screenJoined(undefined)).toBe(false);
  });
});
