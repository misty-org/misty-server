import type { ModelMessage } from "ai";
import { describe, expect, it } from "vitest";
import { finishIssues, mayVerify, verificationNote, type FinishCheck } from "../src/finish-verification.js";

const research = { search: "web_search", fetch: "web_fetch", open: "screen_open" };
const unconfirmed = {
  outcome: "partial" as const,
  summary: "Palace's Winter drop began October 4. A Jordan 11 Low is listed for October 23, but I couldn't confirm the date.",
  remaining: [{ requirement: "Confirm the Jordan 11 Low release date", reason: "the article doesn't give it" }],
};
const check = (overrides: Partial<FinishCheck>): FinishCheck => ({
  request: "What's dropping this week in streetwear?", report: unconfirmed, messages: [] as ModelMessage[],
  turn: 6, limit: 90, long: false, research, readOnly: () => true, ...overrides,
});

describe("finishIssues", () => {
  it("lets a short request's honest partial answer stand", () => {
    expect(finishIssues(check({}))).toEqual([]);
  });

  it("sends a long request's early give-up back", () => {
    expect(finishIssues(check({ long: true })).join("\n")).toMatch(/Confirm the Jordan 11 Low release date/);
  });

  it("gathers every check's finding into one list", () => {
    const issues = finishIssues(check({
      long: true,
      request: "Draft an email in Gmail to my team with this week's drops, and keep each source open in its own tab.",
      report: { outcome: "completed", summary: "I've drafted the email.", remaining: [] },
      tabList: "browser_tab_list",
      readOnly: () => true,
    }));
    expect(issues).toHaveLength(2);
    expect(verificationNote(issues)).toMatch(/^Before your answer goes out[\s\S]*1\. No tool call[\s\S]*2\. The person asked for pages[\s\S]*runs only once/);
  });
});

describe("mayVerify", () => {
  it("verifies once, with turns left", () => {
    expect(mayVerify(false, 10, 90)).toBe(true);
    expect(mayVerify(true, 10, 90)).toBe(false);
    expect(mayVerify(false, 88, 90)).toBe(false);
  });
});

describe("verificationNote", () => {
  it("is undefined when nothing was found", () => {
    expect(verificationNote([])).toBeUndefined();
  });

  it("tells a long run that gave up to keep working, not to stop looking", () => {
    const note = verificationNote(["Not finished yet."], true)!;
    expect(note).toMatch(/Keep working on the open parts/);
    expect(note).not.toMatch(/runs only once|rather than looking for it again/);
  });
});
