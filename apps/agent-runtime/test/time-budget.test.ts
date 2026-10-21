import { describe, expect, it } from "vitest";
import type { ModelMessage } from "ai";
import { estimateMinutes, stillProgressing, timeBudget } from "../src/time-budget.js";

const minutes = (value: number) => value * 60_000;
const checklist = (estimate: number): ModelMessage => ({
  role: "assistant",
  content: [{ type: "tool-call", toolCallId: "c1", toolName: "log_work_checklist", input: { items: [], minutes: estimate } }],
});

describe("timeBudget", () => {
  it("reads the agent's estimate from its checklist", () => {
    expect(estimateMinutes([])).toBeUndefined();
    expect(estimateMinutes([checklist(3), checklist(4)])).toBe(4);
  });

  it("paces a ten-minute task at eight, wraps up at ten, and allows only the answer from thirteen", () => {
    const messages: ModelMessage[] = [];
    expect(timeBudget(minutes(7), messages)).toEqual({ answerOnly: false });
    const pace = timeBudget(minutes(8), messages);
    expect(pace.note).toMatch(/^Time budget \(pace\)/);
    messages.push({ role: "user", content: pace.note! });
    expect(timeBudget(minutes(9), messages).note).toBeUndefined();
    const wrap = timeBudget(minutes(10), messages);
    expect(wrap.note).toMatch(/^Time budget \(wrap\)/);
    messages.push({ role: "user", content: wrap.note! });
    const answer = timeBudget(minutes(13), messages);
    expect(answer).toMatchObject({ answerOnly: true });
    messages.push({ role: "user", content: answer.note! });
    expect(timeBudget(minutes(14), messages)).toEqual({ answerOnly: true, note: undefined });
  });

  it("holds a three-minute lookup to about three minutes", () => {
    expect(timeBudget(minutes(2.5), [], 3).note).toMatch(/^Time budget \(pace\)/);
    expect(timeBudget(minutes(3), [], 3).note).toMatch(/^Time budget \(wrap\)/);
    expect(timeBudget(minutes(4.5), [], 3).answerOnly).toBe(true);
  });

  it("gives deep research its own longer clock", () => {
    const asked: ModelMessage[] = [{ role: "user", content: "Do deep research on solid-state batteries and write a full report." }];
    expect(timeBudget(minutes(20), asked, 45)).toEqual({ answerOnly: false });
    expect(timeBudget(minutes(45), asked, 45).note).toMatch(/^Time budget \(wrap\)/);
    expect(timeBudget(minutes(59), asked, 45).answerOnly).toBe(true);
  });

  it("holds an ordinary task's generous estimate to half an hour", () => {
    const asked: ModelMessage[] = [{ role: "user", content: "Compare three hotels near Disney and keep the pages open." }];
    expect(timeBudget(minutes(30), asked, 45).note).toMatch(/^Time budget \(wrap\)/);
  });

  it("lets a run past its estimate keep going while it still finds new things", () => {
    const going = timeBudget(minutes(20), [], 10, true);
    expect(going).toMatchObject({ answerOnly: false });
    expect(going.note).toMatch(/^Time budget \(progress\)/);
    const told: ModelMessage[] = [{ role: "user", content: going.note! }];
    expect(timeBudget(minutes(25), told, 10, true)).toEqual({ answerOnly: false, note: undefined });
    expect(timeBudget(minutes(25), told, 10, false).answerOnly).toBe(true);
  });
});

const result = (id: string, value: string): ModelMessage => ({
  role: "tool",
  content: [{ type: "tool-result", toolCallId: id, toolName: "browser_snapshot", output: { type: "text", value } }],
});

describe("stillProgressing", () => {
  it("counts results the run has not seen before, not repeats", () => {
    const pages = ["a", "b", "c", "d", "e", "f", "g", "h", "i", "j"].map((page, index) => result(`c${index}`, `page ${page}`));
    expect(stillProgressing(pages)).toBe(true);
    const repeats = [...pages, ...pages.slice(0, 8).map((message, index) => ({ ...message, content: (message.content as never[]).map((part: object) => ({ ...part, toolCallId: `r${index}` })) }) as ModelMessage)];
    expect(stillProgressing(repeats)).toBe(false);
    expect(stillProgressing([result("x", "same [ref=e1]"), result("y", "same [ref=e9]")], 2, 2)).toBe(false);
  });
});
