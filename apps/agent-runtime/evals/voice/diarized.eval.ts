import { existsSync, readFileSync } from "node:fs";
import { expect, it } from "vitest";
import { transcribeDiarizedCall } from "../../src/transcribe-diarized.js";

const fixture = JSON.parse(readFileSync(new URL("./fixtures/two-speakers.json", import.meta.url), "utf8")) as {
  audio: string;
  turns: { speaker: string; text: string }[];
};
const audio = new URL(`./fixtures/${fixture.audio}`, import.meta.url);
const words = (text: string) => text.toLowerCase().replace(/[^\p{L}\p{N}\s]/gu, "").split(/\s+/).filter(Boolean);

// Needs SONIOX_API_KEY and the recorded fixture audio.
it.skipIf(!process.env.SONIOX_API_KEY || !existsSync(audio))("transcribe-diarized gets the speaker turns right", async () => {
  const result = await transcribeDiarizedCall({
    route: { provider: "instance" },
    model: process.env.MISTY_DIARIZED_TRANSCRIPTION_MODEL ?? "soniox/stt-async-v4",
    mediaType: "audio/wav",
    audio: readFileSync(audio).toString("base64"),
    languages: ["en"],
  });
  expect(result.turns).toHaveLength(fixture.turns.length);
  // Speaker labels are arbitrary; the pattern of who spoke when must match.
  const labels = new Map<string, string>();
  result.turns.forEach((turn, index) => {
    const expected = fixture.turns[index]!;
    if (!labels.has(expected.speaker)) labels.set(expected.speaker, turn.speaker);
    expect(turn.speaker).toBe(labels.get(expected.speaker));
    const heard = new Set(words(turn.text));
    const overlap = words(expected.text).filter((word) => heard.has(word)).length / words(expected.text).length;
    expect(overlap).toBeGreaterThan(0.7);
  });
  expect(new Set(labels.values()).size).toBe(2);
});
