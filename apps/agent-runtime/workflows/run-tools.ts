import { WorkflowAgent } from "@ai-sdk/workflow";
import { isStepCount, tool, type ToolSet } from "ai";
import { FatalError } from "workflow";
import { citationInstructions } from "../src/citation-check.js";
import { afterRefusal, unbackedCompletion } from "../src/finish-check.js";
import { InstanceModel } from "../src/instance-model.js";
import { ledgerInputSchema, ledgerInstructions, ledgerTool, recordRows, withLedger } from "../src/ledger.js";
import { modelFamilyNote } from "../src/model-family.js";
import { catalogTools, modelCatalog } from "../src/model-tools.js";
import { withRuntimeInstructions } from "../src/context/prompt-cache.js";
import { summaryMissingNote, taskCompletionInputSchema, taskCompletionInstructions, taskCompletionTool, withAnswerSummary } from "../src/task-completion.js";
import { toolMonitor } from "../src/tool-monitor.js";
import { executionInstructions } from "../src/tool-outcomes.js";
import { agentTools, toolLoadTool, toolUseTool } from "../src/toolsets.js";
import type { MCPRemoteTool } from "../src/types.js";
import { guard, type AgentRun } from "./agent-run.js";
import { verifyFinish } from "./finish-review.js";

export type RunTools = ReturnType<typeof assembleTools>;

/**
 * The run's tools and agent, built from Misty's catalog. A screen that joins the
 * run mid-run (screen_open opening the agent's own tab) rebuilds them, so browser
 * tools reach the model in this run instead of a hand-off to a new one.
 */
export function assembleTools(run: AgentRun, descriptors: MCPRemoteTool[]) {
  const { context, execution, ledger, order, rejected } = run;
  const catalog = modelCatalog(descriptors, [taskCompletionTool, ledgerTool, toolUseTool, toolLoadTool]);
  const advertised = new Set(descriptors.map((descriptor) => descriptor.name));
  const monitor = toolMonitor({ catalog, order, checkpoint: execution.checkpoint, rejected });
  // agent.tool: answer by default; browse, desktop, code and loaded families on request.
  const toolsets = agentTools(catalog, context.prompt);
  const researchTools = { search: catalog.modelName("web.search"), fetch: catalog.modelName("web.fetch"), open: catalog.modelName("screen.open") };
  const tools: ToolSet = {
    ...catalogTools(catalog, async (callId, name, value) => {
      guard(run, callId);
      return await execution.executeCapability(callId, name, value);
    }),
    ...toolsets.tools,
    [ledgerTool]: tool({
      description: "Keep the list of this request's parts and where each stands. Send rows to add or update them: pending, done (with evidence: a kept page's address, a note or file you wrote, or a receipt a tool returned), failed, blocked or skipped (with a reason). It records state; it performs no action.",
      inputSchema: ledgerInputSchema,
      execute: async (sent, options) => {
        guard(run, options.toolCallId);
        // A part is done only with evidence (src/ledger.ts, recordRows).
        const { rows, missing } = recordRows(ledger, sent);
        return {
          status: "recorded", done: rows.filter((row) => row.state === "done").length, open: rows.filter((row) => row.state !== "done" && row.state !== "skipped").length,
          ...(missing.length && { not_done_without_evidence: missing.map((row) => row.requirement.slice(0, 80)), note: "Send these again as done with evidence: the address of a page you kept or read, a note you wrote, or a tool's receipt. For a part that is the answer itself, evidence { kind: \"note\", ref: \"final answer\" }." }),
        };
      },
    }),
    [taskCompletionTool]: tool({
      description: "Report the final task outcome and user-facing answer. Call alone after completing all possible work, or when a real blocker prevents further progress. Leave summary out when your previous response already gave the answer as text. This records a report; it grants no capabilities and performs no external action.",
      inputSchema: taskCompletionInputSchema,
      execute: async (sent, options) => {
        guard(run, options.toolCallId);
        const messages = options.messages ?? [];
        // A finish after a text answer leaves summary out: that answer is the summary, so the
        // person reads it once (src/task-completion.ts).
        const report = withAnswerSummary(sent, messages);
        if (!report) return { status: "failure", tool_error: { message: summaryMissingNote } };
        // Every check runs together, once (src/finish-verification.ts); the next finish stands.
        const note = await verifyFinish(run, report, messages);
        if (note) return { status: "failure", tool_error: { message: note } };
        // A completed report that still claims work no tool did becomes partial when the
        // request named an app (src/finish-check.ts). The ledger decides what remains.
        const reported = withLedger(report, ledger);
        return unbackedCompletion(reported, messages, catalog.readOnly) ? afterRefusal(reported, messages, catalog.readOnly) : reported;
      },
    }),
  };
  const toolNames = Object.keys(tools);
  const instructions = withRuntimeInstructions(context.system, [executionInstructions, taskCompletionInstructions, ledgerInstructions, citationInstructions, toolsets.instructions, modelFamilyNote(context.model_id)].filter(Boolean));
  const agent = new WorkflowAgent({
    id: "misty-space-task-agent",
    model: new InstanceModel(context.model_id, run.identity),
    instructions,
    tools,
    prepareStep: () => ({ activeTools: monitor.activeTools(run.visibleTools) }),
    // One model call per stream lets the coordinator refresh the active-time
    // deadline after durable tool waits. The aggregate loop below owns the cap.
    stopWhen: isStepCount(1),
    // Backoff of 2, 4, 8 and 16 seconds rides out a short network blip to the provider,
    // which failed whole runs at their first call when three quick retries did not.
    maxRetries: 4,
    // The most one turn may write (agents.MaxAgentTurnOutputTokens).
    maxOutputTokens: 32_000,
    reasoning: context.reasoning_effort === "max" ? undefined : context.reasoning_effort || undefined,
    telemetry: { isEnabled: true, recordInputs: false, recordOutputs: false, functionId: "misty.space-task-agent" },
    experimental_onStepStart: async ({ stepNumber }) => {
      if (stepNumber !== 0) throw new FatalError("unexpected_model_step: the pinned adapter exceeded one model call");
    },
    onStepEnd: async ({ finishReason, text }) => {
      try {
        await execution.checkpoint({
          node_id: `model:${run.modelTurn + 1}`, state: "completed", phase: "working", progress: Math.min(90, 15 + run.modelTurn * 6),
          // The control plane projects this model-owned text into the public SSE
          // stream for interactive invocations. Tool-only steps normally have no
          // text, while the final step supplies Markdown as it becomes durable.
          output: { finish_reason: finishReason, text_delta: text },
        });
      } catch (error) {
        run.hookFailure ??= error;
      }
    },
    onToolExecutionStart: monitor.onStart,
    onToolExecutionEnd: monitor.onEnd,
  });
  return { catalog, advertised, monitor, toolsets, researchTools, toolNames, instructions, agent };
}
