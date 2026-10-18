import { expect, it } from "vitest";
import { headings, voiceNotesCall } from "../../src/voice-notes.js";

const turns = [
  { speaker: "You", start: 0, end: 4, text: "Let's decide the dictation shortcut. I think Fn on Mac." },
  { speaker: "Dana", start: 5, end: 9, text: "Agreed. On Windows Right Alt, but some layouts use it as AltGr." },
  { speaker: "Sam", start: 10, end: 15, text: "Then it should be configurable. I can write the settings row this week." },
  { speaker: "You", start: 16, end: 19, text: "Great. Dana, can you check the AltGr filter?" },
  { speaker: "Dana", start: 20, end: 22, text: "Yes, I'll test it on a German keyboard." },
];
const userNotes = "## Decisions\n- **Fn on Mac**\n\n## Open questions\n- AltGr?";
const allowed = new Set(
  `${turns.map((t) => `${t.speaker} ${t.text}`).join(" ")} ${userNotes}`
    .toLowerCase()
    .match(/[\p{L}\p{N}]+/gu),
);
const model = process.env.MISTY_EVAL_NOTES_MODEL ?? "openai/gpt-5-mini";
const live = Boolean(process.env.AI_GATEWAY_API_KEY || process.env.OPENAI_API_KEY);

// Calls a real model through the Gateway.
it.skipIf(!live)("voice-notes keeps the person's headings and invents no facts", async () => {
  const notes = await voiceNotesCall({ route: { provider: "instance" }, model, mode: "notes", turns, userNotes });
  const found = headings(notes.markdown).map((h) => h.toLowerCase());
  expect(found).toEqual(expect.arrayContaining(["decisions", "open questions"]));
  expect(found.indexOf("decisions")).toBeLessThan(found.indexOf("open questions"));
  expect(notes.markdown).toMatch(/\*\*Fn on Mac\*\*/);
  // Every capitalized name and every number must come from the source.
  const names = notes.markdown.match(/\b[A-Z][a-z]{2,}\b/g) ?? [];
  const numbers = notes.markdown.match(/\b\d+\b/g) ?? [];
  const common = new Set(["The", "This", "Decisions", "Open", "Questions", "Action", "Items", "Summary", "Notes", "None", "Mac", "Windows", "Next", "Steps", "Owner", "Yes", "Follow"]);
  for (const word of [...names, ...numbers]) {
    if (!common.has(word)) expect(allowed.has(word.toLowerCase()), `invented "${word}"`).toBe(true);
  }
  const owners = notes.actionItems.map((item) => item.owner).filter(Boolean);
  for (const owner of owners) expect(["You", "Dana", "Sam"]).toContain(owner);
});
