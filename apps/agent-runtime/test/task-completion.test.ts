import { describe, expect, it } from "vitest";
import { finalTaskCompletion, finishMissingNote, lastTextAnswer, taskCompletionInputSchema, taskCompletionInstructions, taskCompletionOutcome, taskCompletionText, taskCompletionTool, withAnswerSummary } from "../src/task-completion.js";

const report = (outcome: "completed" | "partial" | "blocked", remaining: Array<{requirement:string;reason:string}> = []) => ({ outcome, summary: "Task result", remaining });
const finishStep = (output: unknown) => ({ content: [{ type: "tool-call", toolName: taskCompletionTool }, { type: "tool-result", toolName: taskCompletionTool, output }] });
describe("structured task outcome", () => {
  it("reads the pinned WorkflowAgent's executed receipt separate from step content", () => {
    const call = {type: "tool-call", toolName: taskCompletionTool, toolCallId: "finish", input: report("completed")};
    const receipt = {type: "tool-result", toolName: taskCompletionTool, toolCallId: "finish", output: report("completed")};
    expect(finalTaskCompletion([{content:[call]}], [receipt])?.outcome).toBe("completed");
    for (const receipts of [[], [{...receipt,toolCallId:"earlier"}], [{...receipt,toolName:"note.read"}], [{...receipt,type:"tool-error"}], [receipt,receipt], [{...receipt,output:{outcome:"completed"}}]])
      expect(finalTaskCompletion([{content:[call]}], receipts)).toBeUndefined();
    expect(finalTaskCompletion([{content:[call,{type:"tool-call",toolName:"note.create",toolCallId:"write"}]}], [receipt])).toBeUndefined();
    expect(finalTaskCompletion([{content:[call]},{content:[{type:"text"}]}], [receipt])).toBeUndefined();
  });
  it("keeps earlier reads without a deliverable blocked", () => {
    const value = report("blocked", [{ requirement: "Verified GothamChess weekly playlist manifest", reason: "No authorized browser or YouTube reader is available." }]);
    const completed = finalTaskCompletion([
      { content: [{ type: "tool-result", toolName: "note_search", output: { results: [] } }] },
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
    const later = {content:[{type:"tool-result",toolName:"note.create",output:{id:"note"}}]};
    expect(finalTaskCompletion([earlier,later])).toBeUndefined();
    expect(finalTaskCompletion([{content:[...earlier.content,...later.content]}])).toBeUndefined();
    expect(finalTaskCompletion([earlier,later,finishStep(report("partial"))])?.outcome).toBe("partial");
    expect(finalTaskCompletion([{content:[...earlier.content,...earlier.content]}])).toBeUndefined();
  });
});

describe("an answer given as text before agent_finish", () => {
  const answer = "The capital of France is Paris.";
  const finishCall = { role: "assistant" as const, content: [{ type: "tool-call" as const, toolCallId: "f", toolName: taskCompletionTool, input: {} }] };
  const refusal = { role: "tool" as const, content: [{ type: "tool-result" as const, toolCallId: "f", toolName: taskCompletionTool, output: { type: "text" as const, value: "refused" } }] };
  const answered = [
    { role: "user" as const, content: "What is the capital of France?" },
    { role: "assistant" as const, content: [{ type: "tool-call" as const, toolCallId: "a", toolName: "web_search", input: {} }] },
    { role: "tool" as const, content: [{ type: "tool-result" as const, toolCallId: "a", toolName: "web_search", output: { type: "text" as const, value: "Paris" } }] },
    { role: "assistant" as const, content: [{ type: "text" as const, text: answer }] },
    { role: "user" as const, content: finishMissingNote },
  ];
  it("asks for outcome and remaining only, and the instructions no longer demand a repeat", () => {
    expect(finishMissingNote).toMatch(/only outcome and remaining/);
    expect(finishMissingNote).not.toMatch(/same answer in full/);
    expect(taskCompletionInstructions).toMatch(/leave summary out/);
    expect(taskCompletionInputSchema.safeParse({ outcome: "completed", remaining: [] }).success).toBe(true);
  });
  it("uses the text answer as the summary, so the final text is that answer once", () => {
    const report = withAnswerSummary({ outcome: "completed", remaining: [] }, answered);
    expect(report).toEqual({ outcome: "completed", summary: answer, remaining: [] });
    expect(taskCompletionText(report!)).toBe(answer);
    expect(taskCompletionOutcome(finalTaskCompletion([finishStep(report)]))).toEqual({ status: "success" });
    // An empty summary counts as left out, and a summary the model wrote stands.
    expect(withAnswerSummary({ outcome: "completed", summary: "  ", remaining: [] }, answered)?.summary).toBe(answer);
    expect(withAnswerSummary({ outcome: "completed", summary: "Paris.", remaining: [] }, answered)?.summary).toBe("Paris.");
  });
  it("keeps the Still incomplete suffix for a partial run", () => {
    const report = withAnswerSummary({ outcome: "partial", remaining: [{ requirement: "Population", reason: "No source" }] }, answered)!;
    expect(taskCompletionText(report)).toBe(`${answer}\n\nStill incomplete:\n- Population: No source`);
  });
  it("looks past a refused finish sent again, but never takes narration around other tools", () => {
    expect(lastTextAnswer([...answered, finishCall, refusal])).toBe(answer);
    expect(lastTextAnswer([...answered, { role: "assistant", content: [{ type: "text", text: "Let me check one more page." }, { type: "tool-call", toolCallId: "b", toolName: "web_fetch", input: {} }] }])).toBe("");
    expect(lastTextAnswer([{ role: "assistant", content: "Last: read two pages. Next: read the third." }])).toBe("");
    expect(withAnswerSummary({ outcome: "completed", remaining: [] }, answered.slice(0, 3))).toBeUndefined();
  });
});
