import type { ModelMessage } from "ai";
import { describe, expect, it } from "vitest";
import { afterRefusal, isWrite, successfulWrites, unbackedCompletion } from "../src/finish-check.js";

const readOnly = (name: string) => name.endsWith("_query") || name.endsWith("_search");

function call(id: string, toolName: string, input: unknown, output: unknown = { type: "json", value: { successful: true } }): ModelMessage[] {
  return [
    { role: "assistant", content: [{ type: "tool-call", toolCallId: id, toolName, input }] },
    { role: "tool", content: [{ type: "tool-result", toolCallId: id, toolName, output }] },
  ] as ModelMessage[];
}

const ask = (text: string): ModelMessage => ({ role: "user", content: text });
const done = (summary: string) => ({ outcome: "completed", summary });

describe("finish check", () => {
  it("tells reads from writes, including Composio actions", () => {
    expect(isWrite("integration_execute", { tool_slug: "GMAIL_FETCH_EMAILS" }, readOnly)).toBe(false);
    expect(isWrite("integration_execute", { tool_slug: "GMAIL_CREATE_EMAIL_DRAFT" }, readOnly)).toBe(true);
    expect(isWrite("task_create", {}, readOnly)).toBe(true);
    expect(isWrite("task_query", {}, readOnly)).toBe(false);
    expect(isWrite("log_work_write", {}, readOnly)).toBe(false);
  });

  it("refuses a completed draft request with only reads", () => {
    const messages = [ask("Draft one for each candidate."), ...call("a", "integration_execute", { tool_slug: "GMAIL_FETCH_EMAILS" })];
    expect(unbackedCompletion(done("Drafts (not sent): ..."), messages, readOnly)).toMatch(/does not make it/);
  });

  it("accepts it once a write succeeded, and ignores failed writes", () => {
    const failedWrite = call("b", "integration_execute", { tool_slug: "GMAIL_CREATE_EMAIL_DRAFT" }, { type: "error-text", value: "denied" });
    expect(successfulWrites(failedWrite, readOnly)).toBe(0);
    const messages = [ask("Draft a follow-up."), ...call("c", "integration_execute", { tool_slug: "GMAIL_CREATE_EMAIL_DRAFT" })];
    expect(unbackedCompletion(done("I drafted it."), messages, readOnly)).toBeUndefined();
  });

  it("leaves questions, partial reports and unclaimed answers alone", () => {
    const messages = [ask("What in my inbox needs a reply?"), ...call("d", "integration_execute", { tool_slug: "GMAIL_FETCH_EMAILS" })];
    expect(unbackedCompletion(done("Two emails need a reply."), messages, readOnly)).toBeUndefined();
    expect(unbackedCompletion({ outcome: "partial", summary: "I could not save the drafts." }, [ask("Draft them.")], readOnly)).toBeUndefined();
    expect(unbackedCompletion(done("I've created the event."), [ask("Is tomorrow free?")], readOnly)).toMatch(/does not make it/);
  });

  it("turns a second unbacked report into partial when the request named an app", () => {
    const report = { outcome: "completed", summary: "Draft below.", remaining: [] as Array<{ requirement: string; reason: string }> };
    const named = afterRefusal(report, [ask("Draft a short intro email to their CFO.")], readOnly);
    expect(named.outcome).toBe("partial");
    expect(named.remaining).toHaveLength(1);
    expect(afterRefusal(report, [ask("Draft a thank-you note I can paste.")], readOnly).outcome).toBe("completed");
  });

  it("counts opening a tab and running a program as actions, so \"open their channels\" is backed", () => {
    expect(isWrite("screen_open", { url: "https://www.youtube.com/@Fireship" }, () => true)).toBe(true);
    expect(isWrite("browser_run", { code: "return 1;" }, () => true)).toBe(true);
    const done = [ask("Can you open their youtube channels in the browser so i can take a look?"), ...call("a", "screen_open", { url: "https://www.youtube.com/@Fireship" })];
    expect(unbackedCompletion({ outcome: "completed", summary: "All 13 tabs opened." }, done, () => true)).toBeUndefined();
  });
});
