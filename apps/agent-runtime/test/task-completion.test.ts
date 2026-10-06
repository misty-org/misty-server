import { describe, expect, it } from "vitest";
import { finalTaskCompletion, taskCompletionOutcome, taskCompletionText, taskCompletionTool } from "../src/task-completion.js";

const report = (outcome: "completed" | "partial" | "blocked", remaining: Array<{requirement:string;reason:string}> = []) => ({ outcome, summary: "Task result", remaining });
const finishStep = (output: unknown) => ({ content: [{ type: "tool-call", toolName: taskCompletionTool }, { type: "tool-result", toolName: taskCompletionTool, output }] });
describe("structured task outcome", () => {
  it("reads the pinned WorkflowAgent's executed receipt separate from step content", () => {
    const call = {type: "tool-call", toolName: taskCompletionTool, toolCallId: "finish", input: report("completed")};
    const receipt = {type: "tool-result", toolName: taskCompletionTool, toolCallId: "finish", output: report("completed")};
    expect(finalTaskCompletion([{content:[call]}], [receipt])?.outcome).toBe("completed");
    for (const receipts of [[], [{...receipt,toolCallId:"earlier"}], [{...receipt,toolName:"notes.read"}], [{...receipt,type:"tool-error"}], [receipt,receipt], [{...receipt,output:{outcome:"completed"}}]])
      expect(finalTaskCompletion([{content:[call]}], receipts)).toBeUndefined();
    expect(finalTaskCompletion([{content:[call,{type:"tool-call",toolName:"notes.create",toolCallId:"write"}]}], [receipt])).toBeUndefined();
    expect(finalTaskCompletion([{content:[call]},{content:[{type:"text"}]}], [receipt])).toBeUndefined();
  });
  it("keeps earlier reads without a deliverable blocked", () => {
    const value = report("blocked", [{ requirement: "Verified GothamChess weekly playlist manifest", reason: "No authorized browser or YouTube reader is available." }]);
    const completed = finalTaskCompletion([
      { content: [{ type: "tool-result", toolName: "notes_search", output: { results: [] } }] },
      finishStep(value),
    ]);
    expect(taskCompletionOutcome(completed)).toMatchObject({ status: "incomplete", error_code: "task_blocked" });
    expect(taskCompletionText(completed!)).toContain(value.remaining[0]!.reason);
  });
  it("distinguishes verified completion from partial or contradictory claims", () => {
    expect(taskCompletionOutcome(finalTaskCompletion([finishStep(report("completed"))]))).toEqual({ status: "success" });
    expect(taskCompletionOutcome(report("partial", [{ requirement: "Save note", reason: "Permission denied" }]))).toMatchObject({ status: "incomplete", error_code: "task_incomplete" });
    expect(taskCompletionOutcome(report("completed", [{ requirement: "Save note", reason: "Permission denied" }]))).toMatchObject({ status: "incomplete" });
  });
  it("never upgrades plain final prose, a missing report, or malformed report into success", () => {
    for (const steps of [[], [{content:[{type:"text"}]}], [finishStep({outcome:"completed"})]])
      expect(taskCompletionOutcome(finalTaskCompletion(steps))).toMatchObject({status:"incomplete",error_code:"task_outcome_unreported"});
  });
  it("requires the final standalone report after any further work or steering", () => {
    const earlier = finishStep(report("completed"));
    const later = {content:[{type:"tool-result",toolName:"notes.create",output:{id:"note"}}]};
    expect(finalTaskCompletion([earlier,later])).toBeUndefined();
    expect(finalTaskCompletion([{content:[...earlier.content,...later.content]}])).toBeUndefined();
    expect(finalTaskCompletion([earlier,later,finishStep(report("partial"))])?.outcome).toBe("partial");
    expect(finalTaskCompletion([{content:[...earlier.content,...earlier.content]}])).toBeUndefined();
  });
});
