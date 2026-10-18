import { MockLanguageModelV4 } from "ai/test";
import { describe, expect, it } from "vitest";
import { headings, keepUserHeadings, voiceNotesCall, voiceNotesPrompt, voiceNotesSystem } from "../src/voice-notes.js";

const request = {
  route: { provider: "instance" as const },
  model: "openai/test",
  mode: "notes" as const,
  turns: [
    { speaker: "You", start: 0, end: 3, text: "Can we ship the pill on Thursday?" },
    { speaker: "Dana", start: 4, end: 6, text: "Yes, after the design review." },
  ],
  userNotes: "## Decisions\n- **Ship monochrome**\n\n## Follow up\n- Ask Sam",
  template: "## Summary\n## Decisions\n## Action items",
};

const reply = (object: unknown) =>
  new MockLanguageModelV4({
    doGenerate: async () => ({
      content: [{ type: "text", text: JSON.stringify(object) }],
      finishReason: { unified: "stop", raw: undefined },
      usage: { inputTokens: { total: 120, noCache: 120, cacheRead: 0, cacheWrite: 0 }, outputTokens: { total: 40, text: 40, reasoning: 0 } },
      warnings: [],
    }),
  });

describe("voice-notes", () => {
  it("frames the transcript and the person's notes as data with timestamps", () => {
    const prompt = voiceNotesPrompt(request);
    expect(prompt).toContain("Mode: notes");
    expect(prompt).toContain("<my-notes>\n## Decisions");
    expect(prompt).toContain("[0:04] Dana: Yes, after the design review.");
    expect(prompt).toContain("<template>");
    expect(voiceNotesSystem).toMatch(/Never add facts/);
    expect(voiceNotesSystem).toMatch(/Keep every one of their headings/);
  });

  it("puts back any of the person's headings the model dropped", async () => {
    const model = reply({ title: "Pill review", markdown: "## Decisions\n- **Ship monochrome** on Thursday, after review.", actionItems: [{ text: "Book the review", owner: "Dana" }], chapters: [] });
    const notes = await voiceNotesCall(request, undefined, model);
    expect(headings(notes.markdown)).toEqual(["Decisions", "Follow up"]);
    expect(notes.markdown).toContain("- Ask Sam");
    expect(notes.actionItems[0]).toEqual({ text: "Book the review", owner: "Dana" });
    expect(notes.usage).toMatchObject({ inputTokens: 120, outputTokens: 40 });
  });

  it("leaves output alone when headings survived or there were no notes", () => {
    expect(keepUserHeadings(undefined, "## A\nx")).toBe("## A\nx");
    expect(keepUserHeadings("## A\nmine", "## A\nmerged")).toBe("## A\nmerged");
  });

  it("rejects malformed requests", async () => {
    await expect(voiceNotesCall({ ...request, turns: [] })).rejects.toMatchObject({ status: 400 });
  });
});
