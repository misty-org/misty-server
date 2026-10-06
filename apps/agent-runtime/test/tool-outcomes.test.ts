import { describe, expect, it } from "vitest";
import { classifyToolOutcome, endsRun, executionInstructions, unconfirmedToolResultReason } from "../src/tool-outcomes.js";

describe("tool outcome policy", () => {
  it("requires complete generated artifacts before a write", () => {
    expect(executionInstructions).toContain("compose the complete final content before the write");
    expect(executionInstructions).toContain("title, format, count, length, and requested sections");
    expect(executionInstructions).toContain("Never create a placeholder, outline, partial draft, or empty shell");
  });

  it("returns calls that had no effect to the model", () => {
    const write = { success: false, readOnly: false };
    expect(classifyToolOutcome({ ...write, rejected: true, error: "tool_execution_failed: pass space for this change" }).kind).toBe("retry");
    expect(classifyToolOutcome({ success: false, readOnly: true, rejected: false, error: "Misty's tool service is temporarily unavailable." }).kind).toBe("retry");
    expect(classifyToolOutcome({ success: true, readOnly: false, rejected: false, output: { status: "failure", reason: "not_found" } }).kind).toBe("retry");
  });

  it("stops when a write's outcome is unknown or needs the user", () => {
    const write = { success: true, readOnly: false, rejected: false };
    for (const output of [{ status: "uncertain" }, { status: "user_intervention_required" }, { denied: true }, { unavailable: true }])
      expect(classifyToolOutcome({ ...write, output }).kind).toBe("stop");
    expect(classifyToolOutcome({ success: false, readOnly: false, rejected: false, error: "Misty could not reach its tool service." }).kind).toBe("stop");
  });

  it("stops any call once the run loses its authority or budget", () => {
    for (const error of ["permission_denied: This run is no longer authorized to use that tool.", "agent_execution_time_limit: allowance used", "tool_sequence_stopped: earlier failure"]) {
      expect(endsRun(error)).toBe(true);
      expect(classifyToolOutcome({ success: false, readOnly: true, rejected: true, error }).kind).toBe("stop");
    }
  });

  it("hands the run off when it requests a screen", () => {
    const outcome = classifyToolOutcome({ success: true, readOnly: true, rejected: false, output: { status: "screen_requested", message: "Opening a browser." } });
    expect(outcome).toEqual({ kind: "handoff", reason: "Opening a browser." });
    expect(classifyToolOutcome({ success: true, readOnly: true, rejected: false, output: { status: "open" } }).kind).toBe("confirmed");
  });

  it("hands the run off while an app card waits for the user", () => {
    const connect = { status: "waiting_for_user", message: "for the model", user_message: "Connect Gmail with the card above." };
    expect(classifyToolOutcome({ success: true, readOnly: false, rejected: false, output: connect })).toEqual({ kind: "handoff", reason: "Connect Gmail with the card above." });
    const approval = classifyToolOutcome({ success: true, readOnly: false, rejected: false, output: { status: "awaiting_approval" } });
    expect(approval).toEqual({ kind: "handoff", reason: expect.stringContaining("waiting for your approval") });
  });

  it("hands the run off while questions or a proposed plan wait for the user", () => {
    const asked = { status: "awaiting_answer", question_set: "question_1", user_message: "Answer the question above and Misty continues." };
    expect(classifyToolOutcome({ success: true, readOnly: true, rejected: false, output: asked })).toEqual({ kind: "handoff", reason: "Answer the question above and Misty continues." });
    const planned = classifyToolOutcome({ success: true, readOnly: true, rejected: false, output: { status: "plan_proposed", plan: "plan_1", version: 2 } });
    expect(planned).toEqual({ kind: "handoff", reason: expect.stringContaining("Review the plan above") });
    // Answers that arrived while the run waited are an ordinary confirmed result.
    expect(classifyToolOutcome({ success: true, readOnly: true, rejected: false, output: { status: "answered", answers: [] } }).kind).toBe("confirmed");
  });

  it("does not treat denied or unavailable actions as confirmed", () => {
    expect(unconfirmedToolResultReason({ denied: true })).toContain("not approved");
    expect(unconfirmedToolResultReason({ unavailable: true })).toContain("unavailable");
    expect(unconfirmedToolResultReason({ id: "note-1" })).toBe("");
    expect(classifyToolOutcome({ success: true, readOnly: false, rejected: false, output: { id: "note-1" } }).kind).toBe("confirmed");
  });
});
